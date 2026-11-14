// Finishing Close issue POST; own module so it stays out of the initial chunk.
import { post } from './client'
import type { FinishingIssue } from './generated/views'

export function closeHoldingIssue(namespace: string, name: string, milestone: number, issue: number): Promise<FinishingIssue> {
  const p = `/projects/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/milestones/${encodeURIComponent(milestone)}`
  return post(`${p}/issues/${encodeURIComponent(issue)}/close`, {})
}
