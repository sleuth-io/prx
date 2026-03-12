import json

import anthropic
from pydantic import ValidationError

from reviews.config import Config
from reviews.github import PRInfo
from reviews.models import FactorScore, RiskFactors

SYSTEM_PROMPT = """\
You are a senior code reviewer performing risk triage on pull requests.
Your job is to assess the RISK of each PR — not summarize it.

Risk = blast radius in the codebase, not diff size.
A 1-line change to a critical dependency or core business logic is far riskier \
than a 500-line change to a dev tool or test file.

Score each factor from 1 (lowest risk) to 5 (highest risk):

1. **blast_radius**: How many parts of the system could break? Consider the \
centrality of changed files, number of dependents, and whether changes affect \
shared infrastructure.

2. **test_coverage**: How well are these changes covered by tests? Score 1 if \
changes are in test files or well-tested areas. Score 5 if critical paths lack \
tests.

3. **sensitivity**: Are security, auth, payments, data models, or migrations \
touched? Score 1 for dev tooling, config, or cosmetic changes.

4. **complexity**: How subtle are the changes? Could they have non-obvious \
side effects? Score 1 for mechanical/trivial changes.

5. **scope_focus**: Is this PR focused on one concern, or does it touch many \
unrelated areas? Score 1 for single-purpose PRs.

Respond with JSON only. No markdown fences. The JSON must match this schema:
{
  "blast_radius": {"score": <1-5>, "reason": "<one line>"},
  "test_coverage": {"score": <1-5>, "reason": "<one line>"},
  "sensitivity": {"score": <1-5>, "reason": "<one line>"},
  "complexity": {"score": <1-5>, "reason": "<one line>"},
  "scope_focus": {"score": <1-5>, "reason": "<one line>"},
  "risk_summary": "<SHORT one-sentence summary, max 80 chars>"
}

Important scoring guidance:
- Deleting large amounts of business logic is HIGH blast_radius (4-5), not low
- Dependency bumps in dev-only or test-only paths are LOW across all factors
- Consider the ratio of code removed to code added — large net deletions signal risk
"""


def assess_pr(
    *,
    pr: PRInfo,
    codebase_context: str,
    config: Config,
) -> tuple[RiskFactors, str]:
    user_message = _build_user_message(pr, codebase_context)
    client = anthropic.Anthropic(api_key=config.anthropic.api_key)
    response = client.messages.create(
        model=config.anthropic.model,
        max_tokens=1024,
        system=SYSTEM_PROMPT,
        messages=[{"role": "user", "content": user_message}],
    )

    content_block = response.content[0]
    assert hasattr(content_block, "text"), (
        f"Unexpected content block type: {type(content_block)}"
    )
    text = str(content_block.text)
    parsed = json.loads(text)

    risk_summary = parsed.pop("risk_summary", "No summary provided")

    try:
        factors = RiskFactors(**parsed)
    except ValidationError:
        factors = RiskFactors(
            blast_radius=FactorScore(
                **parsed.get("blast_radius", {"score": 3, "reason": "parse error"})
            ),
            test_coverage=FactorScore(
                **parsed.get("test_coverage", {"score": 3, "reason": "parse error"})
            ),
            sensitivity=FactorScore(
                **parsed.get("sensitivity", {"score": 3, "reason": "parse error"})
            ),
            complexity=FactorScore(
                **parsed.get("complexity", {"score": 3, "reason": "parse error"})
            ),
            scope_focus=FactorScore(
                **parsed.get("scope_focus", {"score": 3, "reason": "parse error"})
            ),
        )

    return factors, risk_summary


def _build_user_message(pr: PRInfo, codebase_context: str) -> str:
    return f"""\
## PR #{pr.number}: {pr.title}
Author: {pr.author}
Files changed: {pr.files_changed} | +{pr.additions}/-{pr.deletions}

## Codebase Context
{codebase_context}

## Diff
```
{pr.diff}
```
"""
