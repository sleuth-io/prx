import json
import os
from datetime import datetime
from pathlib import Path

from sqlmodel import Field, Session, SQLModel, create_engine, select

from reviews.models import PRCard, RiskFactors, Verdict


class PRCardRow(SQLModel, table=True):
    __tablename__ = "pr_cards"

    id: int | None = Field(default=None, primary_key=True)
    repo: str = Field(index=True)
    pr_number: int = Field(index=True)
    title: str
    author: str
    url: str
    created_at: str
    additions: int
    deletions: int
    files_changed: int
    factors_json: str
    weighted_score: float
    verdict: str
    risk_summary: str
    review_notes: str = ""
    diff: str = ""
    checks_summary: str = ""
    has_failing_checks: bool = False
    diff_sha: str
    status: str = Field(default="pending")
    scored_at: str = Field(default_factory=lambda: datetime.now().isoformat())


def _db_path() -> Path:
    xdg = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config"))
    db_dir = xdg / "reviews"
    db_dir.mkdir(parents=True, exist_ok=True)
    return db_dir / "reviews.db"


def get_engine(db_path: Path | None = None):
    path = db_path or _db_path()
    engine = create_engine(f"sqlite:///{path}")
    SQLModel.metadata.create_all(engine)
    return engine


def pr_card_to_row(
    card: PRCard, *, diff_sha: str, status: str = "pending"
) -> PRCardRow:
    return PRCardRow(
        repo=card.repo,
        pr_number=card.pr_number,
        title=card.title,
        author=card.author,
        url=card.url,
        created_at=card.created_at.isoformat(),
        additions=card.additions,
        deletions=card.deletions,
        files_changed=card.files_changed,
        factors_json=card.factors.model_dump_json(),
        weighted_score=card.weighted_score,
        verdict=card.verdict.value,
        risk_summary=card.risk_summary,
        review_notes=card.review_notes,
        diff=card.diff,
        checks_summary=card.checks_summary,
        has_failing_checks=card.has_failing_checks,
        diff_sha=diff_sha,
        status=status,
    )


def row_to_pr_card(row: PRCardRow) -> PRCard:
    factors_data = json.loads(row.factors_json)
    return PRCard(
        repo=row.repo,
        pr_number=row.pr_number,
        title=row.title,
        author=row.author,
        url=row.url,
        created_at=datetime.fromisoformat(row.created_at),
        additions=row.additions,
        deletions=row.deletions,
        files_changed=row.files_changed,
        factors=RiskFactors(**factors_data),
        weighted_score=row.weighted_score,
        verdict=Verdict(row.verdict),
        risk_summary=row.risk_summary,
        review_notes=row.review_notes,
        diff=row.diff,
        checks_summary=row.checks_summary,
        has_failing_checks=row.has_failing_checks,
    )


def get_cached_cards(engine, repo: str) -> list[tuple[PRCard, str]]:
    with Session(engine) as session:
        rows = session.exec(select(PRCardRow).where(PRCardRow.repo == repo)).all()
        return [(row_to_pr_card(row), row.status) for row in rows]


def upsert_card(
    engine, card: PRCard, *, diff_sha: str, status: str = "pending"
) -> None:
    with Session(engine) as session:
        existing = session.exec(
            select(PRCardRow).where(
                PRCardRow.repo == card.repo,
                PRCardRow.pr_number == card.pr_number,
            )
        ).first()
        if existing:
            existing.title = card.title
            existing.author = card.author
            existing.url = card.url
            existing.created_at = card.created_at.isoformat()
            existing.additions = card.additions
            existing.deletions = card.deletions
            existing.files_changed = card.files_changed
            existing.factors_json = card.factors.model_dump_json()
            existing.weighted_score = card.weighted_score
            existing.verdict = card.verdict.value
            existing.risk_summary = card.risk_summary
            existing.review_notes = card.review_notes
            existing.diff = card.diff
            existing.checks_summary = card.checks_summary
            existing.has_failing_checks = card.has_failing_checks
            existing.diff_sha = diff_sha
            existing.scored_at = datetime.now().isoformat()
            if status != "pending":
                existing.status = status
        else:
            row = pr_card_to_row(card, diff_sha=diff_sha, status=status)
            session.add(row)
        session.commit()


def update_status(engine, repo: str, pr_number: int, status: str) -> bool:
    with Session(engine) as session:
        row = session.exec(
            select(PRCardRow).where(
                PRCardRow.repo == repo,
                PRCardRow.pr_number == pr_number,
            )
        ).first()
        if not row:
            return False
        row.status = status
        session.commit()
        return True


def get_card_row(engine, repo: str, pr_number: int) -> PRCardRow | None:
    with Session(engine) as session:
        return session.exec(
            select(PRCardRow).where(
                PRCardRow.repo == repo,
                PRCardRow.pr_number == pr_number,
            )
        ).first()
