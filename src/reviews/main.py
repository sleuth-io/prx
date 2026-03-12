import argparse
import asyncio
import sys

from rich.console import Console
from rich.table import Table
from rich.text import Text

from reviews.config import load_config
from reviews.github import detect_repo
from reviews.models import PRCard, Verdict
from reviews.scoring import score_prs


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


def cmd_triage(args: argparse.Namespace) -> None:
    console = Console()

    try:
        config = load_config()
    except (FileNotFoundError, ValueError) as e:
        console.print(f"[red]Error:[/red] {e}")
        sys.exit(1)

    try:
        repo = args.repo if args.repo else detect_repo()
    except Exception as e:
        console.print(f"[red]Error detecting repo:[/red] {e}")
        console.print("Run this command from inside a git repository.")
        sys.exit(1)

    console.print(f"[bold]Reviewing PRs for {repo}...[/bold]")

    try:
        cards = asyncio.run(
            score_prs(
                repo,
                config,
                on_progress=lambda pr: console.print(
                    f"  Scoring #{pr.number}: {pr.title}...", end=""
                ),
            )
        )
    except Exception as e:
        console.print(f"[red]Error scoring PRs:[/red] {e}")
        sys.exit(1)

    if cards:
        print_results(cards, console)
    else:
        console.print("[yellow]No open PRs found.[/yellow]")


def cmd_serve(args: argparse.Namespace) -> None:
    import uvicorn

    from reviews.server import create_app

    try:
        config = load_config()
    except (FileNotFoundError, ValueError) as e:
        print(f"Error: {e}")
        sys.exit(1)

    try:
        repo = args.repo if args.repo else detect_repo()
    except Exception as e:
        print(f"Error detecting repo: {e}")
        print("Provide a repo argument or run from inside a git repository.")
        sys.exit(1)

    repo_dir = args.repo_dir if args.repo_dir else "."
    app = create_app(repo=repo, config=config, repo_dir=repo_dir)
    print(f"Serving reviews for {repo} on http://localhost:{args.port}")
    uvicorn.run(app, host="0.0.0.0", port=args.port)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="PR risk triage - score PRs by blast radius"
    )
    subparsers = parser.add_subparsers(dest="command")

    triage_parser = subparsers.add_parser("triage", help="Score PRs in the terminal")
    triage_parser.add_argument("repo", nargs="?", help="GitHub repo (owner/name)")

    serve_parser = subparsers.add_parser("serve", help="Start the web UI server")
    serve_parser.add_argument("repo", nargs="?", help="GitHub repo (owner/name)")
    serve_parser.add_argument(
        "--port", type=int, default=8000, help="Server port (default: 8000)"
    )
    serve_parser.add_argument(
        "--repo-dir", default=None, help="Local repo directory for code exploration"
    )

    args = parser.parse_args()

    if args.command == "serve":
        cmd_serve(args)
    elif args.command == "triage":
        cmd_triage(args)
    else:
        # Default to triage for backward compatibility
        args.repo = None
        cmd_triage(args)
