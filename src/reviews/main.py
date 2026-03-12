import sys
from datetime import datetime

from rich.console import Console
from rich.table import Table
from rich.text import Text

from reviews.assessor import assess_pr
from reviews.config import Config, Weights, load_config
from reviews.context import gather_context
from reviews.github import PRInfo, detect_repo, fetch_prs
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
    )


def render_score_bar(score: float) -> str:
    filled = round(score)
    return "\u2588" * filled + "\u2591" * (5 - filled)


def verdict_color(verdict: Verdict) -> str:
    match verdict:
        case Verdict.APPROVE:
            return "green"
        case Verdict.REVIEW:
            return "yellow"
        case Verdict.REJECT:
            return "red"


def print_results(cards: list[PRCard], console: Console) -> None:
    cards.sort(key=lambda c: c.weighted_score, reverse=True)

    table = Table(show_header=True, header_style="bold", expand=True, pad_edge=False)
    table.add_column("PR", style="cyan", no_wrap=True, width=8)
    table.add_column("Title / Author", ratio=3)
    table.add_column("Risk", justify="center", width=12)
    table.add_column("Factors", ratio=2)
    table.add_column("Verdict", justify="center", width=10)

    for card in cards:
        f = card.factors
        pr_col = f"#{card.pr_number}"
        title_col = f"{card.title}\n[dim]{card.author} | +{card.additions}/-{card.deletions}[/dim]"
        risk_col = f"{render_score_bar(card.weighted_score)} {card.weighted_score}"

        factors_line = (
            f"blast:{f.blast_radius.score} "
            f"test:{f.test_coverage.score} "
            f"sens:{f.sensitivity.score} "
            f"cmplx:{f.complexity.score} "
            f"scope:{f.scope_focus.score}"
        )
        summary = (
            card.risk_summary[:80] if len(card.risk_summary) > 80 else card.risk_summary
        )
        factors_col = f"{factors_line}\n[dim]{summary}[/dim]"

        color = verdict_color(card.verdict)
        verdict_text = Text(card.verdict.upper(), style=f"bold {color}")

        table.add_row(pr_col, title_col, risk_col, factors_col, verdict_text)

    console.print()
    console.print(table)
    console.print()


def main() -> None:
    console = Console()

    try:
        config = load_config()
    except (FileNotFoundError, ValueError) as e:
        console.print(f"[red]Error:[/red] {e}")
        sys.exit(1)

    try:
        repo = detect_repo()
    except Exception as e:
        console.print(f"[red]Error detecting repo:[/red] {e}")
        console.print("Run this command from inside a git repository.")
        sys.exit(1)

    console.print(f"[bold]Reviewing PRs for {repo}...[/bold]")

    try:
        prs = fetch_prs(repo, config.review.max_diff_chars)
    except Exception as e:
        console.print(f"[red]Error fetching PRs:[/red] {e}")
        sys.exit(1)

    if not prs:
        console.print("[yellow]No open PRs found.[/yellow]")
        return

    console.print(f"Found {len(prs)} open PR(s). Scoring...")

    cards: list[PRCard] = []
    for pr in prs:
        try:
            console.print(f"  Scoring #{pr.number}: {pr.title}...", end="")
            codebase_context = gather_context(pr.diff)
            factors, risk_summary = assess_pr(
                pr=pr,
                codebase_context=codebase_context,
                config=config,
            )
            card = build_pr_card(
                pr=pr,
                factors=factors,
                risk_summary=risk_summary,
                config=config,
                repo=repo,
            )
            cards.append(card)
            console.print(" [green]done[/green]")
        except Exception as e:
            console.print(f" [red]error: {e}[/red]")

    if cards:
        print_results(cards, console)
    else:
        console.print("[red]No PRs could be scored.[/red]")
