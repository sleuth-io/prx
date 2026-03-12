import type { PRCard, ScanStatus } from './types'

const BASE = '/api'

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  })
  if (!res.ok) {
    const detail = await res.text()
    throw new Error(`API error ${res.status}: ${detail}`)
  }
  return res.json()
}

export function fetchPRs(): Promise<PRCard[]> {
  return request('/prs')
}

export function scanPRs(): Promise<ScanStatus> {
  return request('/prs/scan', { method: 'POST' })
}

export function getScanStatus(): Promise<ScanStatus> {
  return request('/prs/scan-status')
}

export function approvePR(prNumber: number): Promise<{ status: string }> {
  return request(`/prs/${prNumber}/approve`, { method: 'POST' })
}

export function rejectPR(prNumber: number): Promise<{ status: string }> {
  return request(`/prs/${prNumber}/reject`, { method: 'POST' })
}

export function skipPR(prNumber: number): Promise<{ status: string }> {
  return request(`/prs/${prNumber}/skip`, { method: 'POST' })
}

export function requestChanges(prNumber: number, body: string): Promise<{ status: string }> {
  return request(`/prs/${prNumber}/request-changes`, {
    method: 'POST',
    body: JSON.stringify({ body }),
  })
}

export function rescorePR(prNumber: number): Promise<{ status: string }> {
  return request(`/prs/${prNumber}/rescore`, { method: 'POST' })
}
