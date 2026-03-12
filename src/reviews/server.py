import asyncio
import logging
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

from reviews.config import Config
from reviews.db import (
    get_cached_cards,
    get_card_row,
    get_engine,
    update_status,
    upsert_card,
)
from reviews.github import approve_pr, fetch_prs, request_changes
from reviews.scoring import (
    compute_diff_sha,
    score_single_pr,
)

logger = logging.getLogger(__name__)


class RequestChangesBody(BaseModel):
    body: str = ""


class PRCardResponse(BaseModel):
    repo: str
    pr_number: int
    title: str
    author: str
    url: str
    created_at: str
    additions: int
    deletions: int
    files_changed: int
    factors: dict
    weighted_score: float
    verdict: str
    risk_summary: str
    review_notes: str
    diff: str
    checks_summary: str
    has_failing_checks: bool
    status: str


class ScanStatus(BaseModel):
    scanning: bool
    total: int
    scored: int


def create_app(*, repo: str, config: Config, repo_dir: str = ".") -> FastAPI:
    engine = get_engine()
    scan_state: dict[str, bool | int] = {
        "scanning": False,
        "total": 0,
        "scored": 0,
    }

    async def _background_scan() -> None:
        scan_state["scanning"] = True
        scan_state["scored"] = 0
        try:
            prs = await asyncio.to_thread(fetch_prs, repo, config.review.max_diff_chars)
            scan_state["total"] = len(prs)
            for pr in prs:
                try:
                    card = await score_single_pr(repo, config, pr, repo_dir=repo_dir)
                    diff_sha = compute_diff_sha(pr.diff)
                    upsert_card(engine, card, diff_sha=diff_sha)
                    scan_state["scored"] = int(scan_state["scored"]) + 1
                    logger.info(
                        "Scored PR #%d (%s/%s)",
                        pr.number,
                        scan_state["scored"],
                        scan_state["total"],
                    )
                except Exception:
                    logger.exception("Failed to score PR #%d", pr.number)
                    scan_state["scored"] = int(scan_state["scored"]) + 1
        finally:
            scan_state["scanning"] = False

    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncGenerator[None]:
        cached = get_cached_cards(engine, repo)
        if not cached:
            logger.info("No cached PRs found, starting background scan")
            asyncio.create_task(_background_scan())
        yield

    app = FastAPI(title="Reviews", version="0.1.0", lifespan=lifespan)

    app.add_middleware(
        CORSMiddleware,  # type: ignore[arg-type]
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    def _card_response(card, status: str) -> PRCardResponse:
        return PRCardResponse(
            repo=card.repo,
            pr_number=card.pr_number,
            title=card.title,
            author=card.author,
            url=card.url,
            created_at=card.created_at.isoformat(),
            additions=card.additions,
            deletions=card.deletions,
            files_changed=card.files_changed,
            factors=card.factors.model_dump(),
            weighted_score=card.weighted_score,
            verdict=card.verdict.value,
            risk_summary=card.risk_summary,
            review_notes=card.review_notes,
            diff=card.diff,
            checks_summary=card.checks_summary,
            has_failing_checks=card.has_failing_checks,
            status=status,
        )

    @app.get("/api/prs")
    def list_prs() -> list[PRCardResponse]:
        cached = get_cached_cards(engine, repo)
        results = [_card_response(card, status) for card, status in cached]
        results.sort(key=lambda r: r.weighted_score, reverse=True)
        return results

    @app.get("/api/prs/scan-status")
    def get_scan_status() -> ScanStatus:
        return ScanStatus(
            scanning=bool(scan_state["scanning"]),
            total=int(scan_state["total"]),
            scored=int(scan_state["scored"]),
        )

    @app.post("/api/prs/scan")
    async def scan_prs() -> ScanStatus:
        if scan_state["scanning"]:
            return ScanStatus(
                scanning=bool(scan_state["scanning"]),
                total=int(scan_state["total"]),
                scored=int(scan_state["scored"]),
            )
        asyncio.create_task(_background_scan())
        await asyncio.sleep(0.1)
        return ScanStatus(
            scanning=bool(scan_state["scanning"]),
            total=int(scan_state["total"]),
            scored=int(scan_state["scored"]),
        )

    @app.post("/api/prs/{pr_number}/approve")
    def approve(pr_number: int) -> dict:
        row = get_card_row(engine, repo, pr_number)
        if row and row.has_failing_checks:
            raise HTTPException(
                status_code=422,
                detail="Cannot approve: PR has failing CI checks",
            )
        try:
            approve_pr(repo, pr_number)
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e)) from e
        update_status(engine, repo, pr_number, "approved")
        return {"status": "approved", "pr_number": pr_number}

    @app.post("/api/prs/{pr_number}/reject")
    def reject(pr_number: int) -> dict:
        if not update_status(engine, repo, pr_number, "rejected"):
            raise HTTPException(status_code=404, detail="PR not found")
        return {"status": "rejected", "pr_number": pr_number}

    @app.post("/api/prs/{pr_number}/skip")
    def skip(pr_number: int) -> dict:
        if not update_status(engine, repo, pr_number, "skipped"):
            raise HTTPException(status_code=404, detail="PR not found")
        return {"status": "skipped", "pr_number": pr_number}

    @app.post("/api/prs/{pr_number}/request-changes")
    def request_changes_endpoint(pr_number: int, payload: RequestChangesBody) -> dict:
        try:
            request_changes(repo, pr_number, payload.body)
        except Exception as e:
            raise HTTPException(status_code=500, detail=str(e)) from e
        update_status(engine, repo, pr_number, "changes_requested")
        return {"status": "changes_requested", "pr_number": pr_number}

    @app.post("/api/prs/{pr_number}/rescore")
    async def rescore(pr_number: int) -> dict:
        prs = fetch_prs(repo, config.review.max_diff_chars)
        pr = next((p for p in prs if p.number == pr_number), None)
        if not pr:
            raise HTTPException(status_code=404, detail="PR not found on GitHub")
        card = await score_single_pr(repo, config, pr, repo_dir=repo_dir)
        diff_sha = compute_diff_sha(pr.diff)
        upsert_card(engine, card, diff_sha=diff_sha)
        return {"status": "rescored", "pr_number": pr_number}

    # Serve Vue frontend in production
    dist_dir = Path(__file__).parent.parent.parent / "frontend" / "dist"
    if dist_dir.exists():
        app.mount("/", StaticFiles(directory=str(dist_dir), html=True), name="frontend")

    return app
