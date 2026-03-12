export interface FactorScore {
  score: number
  reason: string
}

export interface RiskFactors {
  blast_radius: FactorScore
  test_coverage: FactorScore
  sensitivity: FactorScore
  complexity: FactorScore
  scope_focus: FactorScore
}

export interface PRCard {
  repo: string
  pr_number: number
  title: string
  author: string
  url: string
  created_at: string
  additions: number
  deletions: number
  files_changed: number
  factors: RiskFactors
  weighted_score: number
  verdict: 'approve' | 'review' | 'reject'
  risk_summary: string
  review_notes: string
  diff: string
  checks_summary: string
  has_failing_checks: boolean
  status: 'pending' | 'approved' | 'rejected' | 'skipped' | 'changes_requested'
}

export interface ScanStatus {
  scanning: boolean
  total: number
  scored: number
}
