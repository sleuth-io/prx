import hashlib
from collections.abc import Callable
from datetime import datetime

from reviews.assessor import assess_pr
from reviews.config import Config, Weights
from reviews.context import gather_context
from reviews.github import PRInfo, fetch_prs
from reviews.models import PRCard, RiskFactors, Verdict


def compute_weighted_score(factors: RiskFactors, weights: Weights) -> float:
    weighted_sum = (
        factors.blast_radius.score * weights.blast_radius
        + factors.test_coverage.score * weights.test_coverage
        + factors.sensitivity.score * weights.sensitivity
        + factors.complexity.score * weights.complexity
        + factors.scope_focus.score * weights.scope_focus
    )
    total_weight = (
        weights.blast_radius
        + weights.test_coverage
        + weights.sensitivity
        + weights.complexity
        + weights.scope_focus
    )
    return round(weighted_sum / total_weight, 1) if total_weight > 0 else 0.0


def compute_verdict(score: float, config: Config) -> Verdict:
    if score < config.thresholds.approve_below:
        return Verdict.APPROVE
    if score > config.thresholds.review_above:
        return Verdict.REVIEW
    return Verdict.REVIEW


def build_pr_card(
    *,
    pr: PRInfo,
    factors: RiskFactors,
    risk_summary: str,
    review_notes: str,
    config: Config,
    repo: str,
) -> PRCard:
    weighted_score = compute_weighted_score(factors, config.weights)
    verdict = compute_verdict(weighted_score, config)
    return PRCard(
        repo=repo,
        pr_number=pr.number,
        title=pr.title,
        author=pr.author,
        url=pr.url,
        created_at=datetime.fromisoformat(pr.created_at),
        additions=pr.additions,
        deletions=pr.deletions,
        files_changed=pr.files_changed,
        factors=factors,
        weighted_score=weighted_score,
        verdict=verdict,
        risk_summary=risk_summary,
        review_notes=review_notes,
        diff=pr.diff,
        checks_summary=pr.checks_summary,
        has_failing_checks=pr.has_failing_checks,
    )


def compute_diff_sha(diff: str) -> str:
    return hashlib.sha256(diff.encode()).hexdigest()[:16]


async def score_prs(
    repo: str,
    config: Config,
    *,
    repo_dir: str = ".",
    on_progress: Callable[[PRInfo], None] | None = None,
) -> list[PRCard]:
    prs = fetch_prs(repo, config.review.max_diff_chars)
    cards: list[PRCard] = []
    for pr in prs:
        if on_progress:
            on_progress(pr)
        codebase_context = gather_context(pr.diff)
        factors, risk_summary, review_notes = await assess_pr(
            pr=pr,
            codebase_context=codebase_context,
            config=config,
            repo_dir=repo_dir,
        )
        card = build_pr_card(
            pr=pr,
            factors=factors,
            risk_summary=risk_summary,
            review_notes=review_notes,
            config=config,
            repo=repo,
        )
        cards.append(card)
    return cards


async def score_single_pr(
    repo: str,
    config: Config,
    pr: PRInfo,
    *,
    repo_dir: str = ".",
) -> PRCard:
    codebase_context = gather_context(pr.diff)
    factors, risk_summary, review_notes = await assess_pr(
        pr=pr,
        codebase_context=codebase_context,
        config=config,
        repo_dir=repo_dir,
    )
    return build_pr_card(
        pr=pr,
        factors=factors,
        risk_summary=risk_summary,
        review_notes=review_notes,
        config=config,
        repo=repo,
    )
