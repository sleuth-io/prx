import glob
import logging
import os
import subprocess
from dataclasses import dataclass

from pydantic import BaseModel, Field
from pydantic_ai import Agent, RunContext
from pydantic_ai.models.anthropic import AnthropicModel
from pydantic_ai.providers.anthropic import AnthropicProvider

from reviews.config import Config
from reviews.github import PRInfo
from reviews.models import FactorScore, RiskFactors

logger = logging.getLogger(__name__)

MAX_FILE_CHARS = 10000
MAX_SEARCH_RESULTS = 30


@dataclass
class AssessorDeps:
    repo_dir: str


class AssessmentResult(BaseModel):
    blast_radius: FactorScore = Field(
        description="How many parts of the system could break (1-5)"
    )
    test_coverage: FactorScore = Field(
        description="How well changes are covered by tests (1-5)"
    )
    sensitivity: FactorScore = Field(
        description="Are security/auth/payments/data models touched (1-5)"
    )
    complexity: FactorScore = Field(
        description="How subtle are the changes, non-obvious side effects (1-5)"
    )
    scope_focus: FactorScore = Field(
        description="Single concern vs multi-area changes (1-5)"
    )
    risk_summary: str = Field(description="One-sentence summary, max 80 chars")
    review_notes: str = Field(
        description=(
            "Bullet-point notes for the reviewer (3-5 bullets, each starting with '- '). "
            "First bullet: what the PR does. Remaining bullets: what to focus on during "
            "review, key risks, and anything surprising. Keep each bullet to one line."
        )
    )


SYSTEM_PROMPT = """\
You are a senior code reviewer performing risk triage on pull requests.
Your job is to assess the RISK of each PR — not summarize it.

Risk = blast radius in the codebase, not diff size.
A 1-line change to a critical dependency or core business logic is far riskier \
than a 500-line change to a dev tool or test file.

You have tools to explore the codebase. Use them to:
- Read the full source of modified files (not just diff hunks)
- Check callers/usages of changed functions or classes
- Verify test coverage for modified code paths
- Examine related config, migration, or infrastructure files

Be efficient — only explore when the diff raises questions you can't answer \
from context alone. Don't read every file; focus on high-signal investigations.

Score each factor from 1 (lowest risk) to 5 (highest risk):

1. **blast_radius**: How many parts of the system could break? Consider the \
centrality of changed files, number of dependents, and whether changes affect \
shared infrastructure.

2. **test_coverage**: How well are these changes covered by tests? Score 1 if \
changes are in test files or well-tested areas. Score 5 if critical paths lack \
tests. CI check results are provided — failing tests are a strong signal for \
score 4-5.

3. **sensitivity**: Are security, auth, payments, data models, or migrations \
touched? Score 1 for dev tooling, config, or cosmetic changes.

4. **complexity**: How subtle are the changes? Could they have non-obvious \
side effects? Score 1 for mechanical/trivial changes.

5. **scope_focus**: Is this PR focused on one concern, or does it touch many \
unrelated areas? Score 1 for single-purpose PRs.

Important scoring guidance:
- Deleting large amounts of business logic is HIGH blast_radius (4-5), not low
- Dependency bumps in dev-only or test-only paths are LOW across all factors
- Consider the ratio of code removed to code added — large net deletions signal risk
- Failing CI checks should significantly increase test_coverage score
- Review comments from other developers highlight areas of concern — weigh them
- The PR description often explains motivation and known risks — factor it in

For `review_notes`, write 3-5 bullet points (each starting with '- '):
- First bullet: what the PR actually does (not just the title)
- Remaining bullets: what to focus on during review, key risks, anything surprising
- Keep each bullet to one concise line
- Example format:
  - Adds retry logic to the payment webhook handler
  - Focus on the new timeout value in webhook_handler.py — 30s may be too aggressive
  - No tests for the retry path when the upstream returns 503

For `risk_summary`, write a single plain-text sentence (no JSON, no structured data). \
Max 80 characters. Example: "Risky auth change with no test coverage"
"""

assessor_agent = Agent(
    deps_type=AssessorDeps,
    output_type=AssessmentResult,
    instructions=SYSTEM_PROMPT,
)


@assessor_agent.tool
def read_file(
    ctx: RunContext[AssessorDeps],
    path: str,
    start_line: int | None = None,
    end_line: int | None = None,
) -> str:
    """Read the contents of a file in the repository.

    Use this to examine source code referenced in the diff,
    check callers of modified functions, or read test files.

    Args:
        path: File path relative to the repository root.
        start_line: Optional start line (1-based). Omit to read from the beginning.
        end_line: Optional end line (1-based). Omit to read to the end.
    """
    full_path = os.path.normpath(os.path.join(ctx.deps.repo_dir, path))
    if not full_path.startswith(os.path.normpath(ctx.deps.repo_dir)):
        return "Error: path traversal not allowed"
    try:
        with open(full_path) as f:
            lines = f.readlines()
    except FileNotFoundError:
        return f"Error: file not found: {path}"
    except OSError as e:
        return f"Error reading file: {e}"

    start = (start_line - 1) if start_line else 0
    end = end_line if end_line else len(lines)
    selected = lines[start:end]

    content = ""
    for i, line in enumerate(selected, start=start + 1):
        content += f"{i}: {line}"

    if len(content) > MAX_FILE_CHARS:
        content = content[:MAX_FILE_CHARS] + "\n... [truncated]"
    return content


@assessor_agent.tool
def search_code(
    ctx: RunContext[AssessorDeps],
    pattern: str,
    file_glob: str | None = None,
) -> str:
    """Search for a pattern across the codebase using grep.

    Use this to find callers of a function, usages of a class,
    imports of a module, or any text pattern.

    Args:
        pattern: Regex pattern to search for.
        file_glob: Optional glob to filter files, e.g. '*.py' or '*.ts'.
    """
    cmd = ["grep", "-rn", "--include", file_glob or "*", "-E", pattern, "."]
    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            cwd=ctx.deps.repo_dir,
            timeout=15,
        )
    except subprocess.TimeoutExpired:
        return "Error: search timed out"

    lines = result.stdout.strip().split("\n")
    lines = [line for line in lines if line]
    if not lines:
        return "No matches found"
    if len(lines) > MAX_SEARCH_RESULTS:
        lines = lines[:MAX_SEARCH_RESULTS]
        lines.append(f"... ({MAX_SEARCH_RESULTS} results shown, more omitted)")
    return "\n".join(lines)


@assessor_agent.tool
def list_files(
    ctx: RunContext[AssessorDeps],
    pattern: str,
) -> str:
    """List files in the repository matching a glob pattern.

    Use this to discover project structure, find test files,
    or locate related modules.

    Args:
        pattern: Glob pattern, e.g. 'src/**/*.py' or 'tests/'.
    """
    full_pattern = os.path.join(ctx.deps.repo_dir, pattern)
    matches = sorted(glob.glob(full_pattern, recursive=True))
    rel_paths = [
        os.path.relpath(m, ctx.deps.repo_dir) for m in matches if os.path.isfile(m)
    ]
    if not rel_paths:
        return "No files matched"
    if len(rel_paths) > 100:
        rel_paths = rel_paths[:100]
        rel_paths.append("... (100 results shown, more omitted)")
    return "\n".join(rel_paths)


def _create_model(config: Config) -> AnthropicModel:
    provider = AnthropicProvider(api_key=config.anthropic.api_key)
    return AnthropicModel(config.anthropic.model, provider=provider)


async def assess_pr(
    *,
    pr: PRInfo,
    codebase_context: str,
    config: Config,
    repo_dir: str = ".",
) -> tuple[RiskFactors, str, str]:
    model = _create_model(config)
    user_message = _build_user_message(pr, codebase_context)
    deps = AssessorDeps(repo_dir=repo_dir)

    result = await assessor_agent.run(user_message, deps=deps, model=model)
    assessment: AssessmentResult = result.output  # type: ignore[assignment]

    factors = RiskFactors(
        blast_radius=assessment.blast_radius,
        test_coverage=assessment.test_coverage,
        sensitivity=assessment.sensitivity,
        complexity=assessment.complexity,
        scope_focus=assessment.scope_focus,
    )
    return factors, assessment.risk_summary, assessment.review_notes


def _build_user_message(pr: PRInfo, codebase_context: str) -> str:
    sections = [
        f"## PR #{pr.number}: {pr.title}",
        f"Author: {pr.author}",
        f"Files changed: {pr.files_changed} | +{pr.additions}/-{pr.deletions}",
    ]

    if pr.body:
        sections.append(f"\n## PR Description\n{pr.body[:2000]}")

    sections.append(f"\n## CI Checks\n{pr.checks_summary}")

    if pr.comments:
        comment_lines = []
        for c in pr.comments[:10]:
            body_preview = c.body[:200] if len(c.body) > 200 else c.body
            comment_lines.append(f"- **{c.author}** ({c.state}): {body_preview}")
        sections.append("\n## Review Comments\n" + "\n".join(comment_lines))

    sections.append(f"\n## Codebase Context\n{codebase_context}")
    sections.append(f"\n## Diff\n```\n{pr.diff}\n```")

    sections.append(
        "\n## Instructions\n"
        "Use the tools to explore the codebase if needed — check callers of "
        "modified functions, verify test coverage, or read related files. "
        "When you have enough information, provide your assessment."
    )

    return "\n".join(sections)
