import type { Tone } from './format.ts'
import type { MilestoneIssueRow, ReleaseView } from './types'

// The release rows of a milestone's Issues tab (R12, decision 15): a
// release is a row between the issues. Why, reason and titles are untrusted
// text; the component renders them as text.

export type IssueTabRow =
  | { kind: 'issue'; key: string; issue: MilestoneIssueRow }
  | { kind: 'release'; key: string; release: ReleaseView }

// releaseAnchor is the issue a release row follows: its afterIssue, else
// the issue its contents end with (owner, final, sweep: D296-P1), else none.
export function releaseAnchor(r: Pick<ReleaseView, 'afterIssue' | 'issues'>): number | undefined {
  if (r.afterIssue) return r.afterIssue
  const n = r.issues?.length ?? 0
  return n > 0 ? r.issues[n - 1] : undefined
}

// interleaveReleases puts each release after the row of its anchor issue,
// several after one issue by id; releases without an anchor, or whose
// anchor is not an issue of the list, go at the end, by id.
export function interleaveReleases(
  issues: readonly MilestoneIssueRow[],
  releases: readonly ReleaseView[] | null | undefined,
): IssueTabRow[] {
  const known = new Set(issues.map((i) => i.iid))
  const after = new Map<number, ReleaseView[]>()
  const tail: ReleaseView[] = []
  for (const r of [...(releases ?? [])].sort((a, b) => a.id - b.id)) {
    const a = releaseAnchor(r)
    if (a !== undefined && known.has(a)) {
      const list = after.get(a) ?? []
      list.push(r)
      after.set(a, list)
    } else {
      tail.push(r)
    }
  }
  const rel = (r: ReleaseView): IssueTabRow => ({ kind: 'release', key: `r${r.id}`, release: r })
  const rows: IssueTabRow[] = []
  for (const i of issues) {
    rows.push({ kind: 'issue', key: `i${i.iid}`, issue: i })
    for (const r of after.get(i.iid) ?? []) rows.push(rel(r))
    after.delete(i.iid) // an issue listed twice gets its releases once
  }
  for (const r of tail) rows.push(rel(r))
  return rows
}

// releaseDone: a release that shipped, with its version.
export function releaseDone(r: Pick<ReleaseView, 'state'>): boolean {
  return r.state === 'Released' || r.state === 'AlreadyReleased'
}

// releaseHeadline is the row's main text. A done release shows its version
// (linked by the component), the rest say where they stand.
export function releaseHeadline(r: Pick<ReleaseView, 'state' | 'afterIssue' | 'issues' | 'version'>): string {
  const a = releaseAnchor(r)
  switch (r.state) {
    case 'Planned':
    case 'Waiting':
      return a !== undefined ? `Release after #${a}` : 'Release at the end'
    case 'Running':
      return 'Releasing…'
    case 'Released':
    case 'AlreadyReleased':
      return r.version || 'Released'
    case 'NotNeeded':
      return 'No release needed'
    case 'NotApplicable':
      return 'No release: the project does not tag'
    case 'Dropped':
      return a !== undefined ? `Release after #${a} dropped` : 'Release dropped'
    case 'Failed':
      return 'Release failed'
    default:
      return `Release: ${r.state}`
  }
}

// releaseStateLabel is the state badge's text.
export function releaseStateLabel(s: ReleaseView['state']): string {
  switch (s) {
    case 'AlreadyReleased':
      return 'already released'
    case 'NotNeeded':
      return 'not needed'
    case 'NotApplicable':
      return 'n/a'
    default:
      return s.toLowerCase()
  }
}

// releaseTone is the state badge's tone; a muted row for the releases that
// did not happen, a warning for a failed one.
export function releaseTone(s: ReleaseView['state']): Tone {
  switch (s) {
    case 'Planned':
      return 'neutral'
    case 'Waiting':
      return 'info'
    case 'Running':
      return 'progress'
    case 'Released':
    case 'AlreadyReleased':
      return 'success'
    case 'Failed':
      return 'warning'
    default:
      return 'muted'
  }
}

// releaseMuted: the row of a release that did not happen.
export function releaseMuted(s: ReleaseView['state']): boolean {
  return s === 'NotNeeded' || s === 'NotApplicable' || s === 'Dropped' || s === 'Failed'
}

// releaseBy says who decided: "planner", "owner jo".
export function releaseBy(r: Pick<ReleaseView, 'by' | 'actor'>): string {
  return r.actor ? `${r.by} ${r.actor}` : r.by
}

// releaseNote is the muted line: the decision's why and the operator's
// reason, empty without either.
export function releaseNote(r: Pick<ReleaseView, 'why' | 'reason'>): string {
  return [r.why, r.reason].filter((s) => s && s.trim()).join(' · ')
}

// releaseTime is the time a row shows: the end of a done or ended release,
// else its start, else the decision.
export function releaseTime(r: Pick<ReleaseView, 'endedAt' | 'startedAt' | 'decidedAt'>): string | undefined {
  return r.endedAt || r.startedAt || r.decidedAt || undefined
}
