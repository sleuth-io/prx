from datetime import datetime
from enum import StrEnum

from pydantic import BaseModel


class Verdict(StrEnum):
    APPROVE = "approve"
    REVIEW = "review"
    REJECT = "reject"


class FactorScore(BaseModel):
    score: int
    reason: str


class RiskFactors(BaseModel):
    blast_radius: FactorScore
    test_coverage: FactorScore
    sensitivity: FactorScore
    complexity: FactorScore
    scope_focus: FactorScore


class PRCard(BaseModel):
    repo: str
    pr_number: int
    title: str
    author: str
    url: str
    created_at: datetime
    additions: int
    deletions: int
    files_changed: int
    factors: RiskFactors
    weighted_score: float
    verdict: Verdict
    risk_summary: str
