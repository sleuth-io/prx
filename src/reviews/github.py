import json
import subprocess
from dataclasses import dataclass


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


def detect_repo() -> str:
    result = subprocess.run(
        ["git", "remote", "get-url", "origin"],
        capture_output=True,
        text=True,
        check=True,
    )
    url = result.stdout.strip()
    # Handle SSH: git@github.com:owner/repo.git
    if url.startswith("git@"):
        path = url.split(":", 1)[1]
    # Handle HTTPS: https://github.com/owner/repo.git
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
            "number,title,author,url,createdAt,additions,deletions,files",
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


def fetch_prs(repo: str, max_diff_chars: int) -> list[PRInfo]:
    raw_prs = list_open_prs(repo)
    prs: list[PRInfo] = []
    for pr in raw_prs:
        diff = get_pr_diff(repo, pr["number"], max_diff_chars)
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
            )
        )
    return prs
