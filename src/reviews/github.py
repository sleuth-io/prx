import json
import subprocess
from dataclasses import dataclass, field


@dataclass
class CheckStatus:
    name: str
    status: str
    conclusion: str


@dataclass
class ReviewComment:
    author: str
    body: str
    state: str
    submitted_at: str


@dataclass
class PRInfo:
    number: int
    title: str
    author: str
    url: str
    created_at: str
    additions: int
    deletions: int
    files_changed: int
    diff: str
    body: str = ""
    checks: list[CheckStatus] = field(default_factory=list)
    comments: list[ReviewComment] = field(default_factory=list)

    @property
    def has_failing_checks(self) -> bool:
        return any(
            c.conclusion in ("failure", "cancelled", "timed_out") for c in self.checks
        )

    @property
    def checks_summary(self) -> str:
        if not self.checks:
            return "No CI checks"
        failed = [
            c
            for c in self.checks
            if c.conclusion in ("failure", "cancelled", "timed_out")
        ]
        passed = [c for c in self.checks if c.conclusion == "success"]
        pending = [c for c in self.checks if c.conclusion in ("", "pending", "neutral")]
        parts = []
        if passed:
            parts.append(f"{len(passed)} passed")
        if failed:
            parts.append(f"{len(failed)} failed: {', '.join(c.name for c in failed)}")
        if pending:
            parts.append(f"{len(pending)} pending")
        return "; ".join(parts)


def detect_repo() -> str:
    result = subprocess.run(
        ["git", "remote", "get-url", "origin"],
        capture_output=True,
        text=True,
        check=True,
    )
    url = result.stdout.strip()
    if url.startswith("git@"):
        path = url.split(":", 1)[1]
    elif "github.com" in url:
        path = url.split("github.com/", 1)[1]
    else:
        raise ValueError(f"Cannot parse GitHub repo from remote URL: {url}")
    return path.removesuffix(".git")


def list_open_prs(repo: str) -> list[dict]:
    result = subprocess.run(
        [
            "gh",
            "pr",
            "list",
            "--repo",
            repo,
            "--state",
            "open",
            "--json",
            "number,title,author,url,createdAt,additions,deletions,files,body",
            "--limit",
            "50",
        ],
        capture_output=True,
        text=True,
        check=True,
    )
    return json.loads(result.stdout)


def get_pr_diff(repo: str, pr_number: int, max_chars: int) -> str:
    result = subprocess.run(
        ["gh", "pr", "diff", str(pr_number), "--repo", repo],
        capture_output=True,
        text=True,
        check=True,
    )
    diff = result.stdout
    if len(diff) > max_chars:
        diff = diff[:max_chars] + "\n... [diff truncated]"
    return diff


def get_pr_checks(repo: str, pr_number: int) -> list[CheckStatus]:
    result = subprocess.run(
        [
            "gh",
            "pr",
            "checks",
            str(pr_number),
            "--repo",
            repo,
            "--json",
            "name,state",
        ],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0 or not result.stdout.strip():
        return []
    raw = json.loads(result.stdout)
    return [
        CheckStatus(
            name=c.get("name", ""),
            status=c.get("state", "").lower(),
            conclusion=c.get("state", "").lower(),
        )
        for c in raw
    ]


def get_pr_reviews(repo: str, pr_number: int) -> list[ReviewComment]:
    result = subprocess.run(
        [
            "gh",
            "api",
            f"repos/{repo}/pulls/{pr_number}/reviews",
            "--jq",
            "[.[] | {author: .user.login, body: .body, state: .state, submitted_at: .submitted_at}]",
        ],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0 or not result.stdout.strip():
        return []
    raw = json.loads(result.stdout)
    return [
        ReviewComment(
            author=r.get("author", ""),
            body=r.get("body", ""),
            state=r.get("state", ""),
            submitted_at=r.get("submitted_at", ""),
        )
        for r in raw
        if r.get("body")
    ]


def approve_pr(repo: str, pr_number: int) -> None:
    subprocess.run(
        ["gh", "pr", "review", str(pr_number), "--repo", repo, "--approve"],
        capture_output=True,
        text=True,
        check=True,
    )


def request_changes(repo: str, pr_number: int, body: str) -> None:
    subprocess.run(
        [
            "gh",
            "pr",
            "review",
            str(pr_number),
            "--repo",
            repo,
            "--request-changes",
            "--body",
            body,
        ],
        capture_output=True,
        text=True,
        check=True,
    )


def fetch_prs(repo: str, max_diff_chars: int) -> list[PRInfo]:
    raw_prs = list_open_prs(repo)
    prs: list[PRInfo] = []
    for pr in raw_prs:
        diff = get_pr_diff(repo, pr["number"], max_diff_chars)
        checks = get_pr_checks(repo, pr["number"])
        reviews = get_pr_reviews(repo, pr["number"])
        author = pr.get("author", {})
        author_login = (
            author.get("login", "unknown") if isinstance(author, dict) else str(author)
        )
        prs.append(
            PRInfo(
                number=pr["number"],
                title=pr["title"],
                author=author_login,
                url=pr["url"],
                created_at=pr["createdAt"],
                additions=pr["additions"],
                deletions=pr["deletions"],
                files_changed=len(pr.get("files", [])),
                diff=diff,
                body=pr.get("body", ""),
                checks=checks,
                comments=reviews,
            )
        )
    return prs
