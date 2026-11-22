import { relTime, shortSHA } from './format.ts'
import type { Facts } from './generated/problem'

// FactPart is a piece of a facts row: text, a GitLab link (href) or a Desk
// link (to: a task's logs tab).
interface FactPart {
  text: string
  href?: string
  to?: { name: 'task'; params: { namespace: string; name: string; task: string }; query: { tab: 'logs' } }
}
export interface FactRow {
  key: 'issue' | 'mr' | 'pipeline' | 'run' | 'saved' | 'blocked'
  parts: FactPart[]
}

const n = (k: number, one: string) => `${k} ${one}${k === 1 ? '' : 's'}`

// factRows turns a problem's facts (R8, Decision 3) into the strip's rows;
// a fact the operator did not collect leaves no row. Error text is the
// agent's: plain text.
export function factRows(f: Facts, project: { namespace: string; name: string }, now: number): FactRow[] {
  const rows: FactRow[] = [{ key: 'issue', parts: [{ text: `#${f.iid}`, href: f.issue.url }] }]
  const mr = f.mergeRequest
  if (mr) {
    const pushed = mr.pushedAt ? `pushed ${relTime(mr.pushedAt, now)}` : ''
    const info = [mr.headSHA && shortSHA(mr.headSHA), n(mr.commitsAhead ?? 0, 'commit') + ' ahead', pushed]
    rows.push({ key: 'mr', parts: [{ text: `!${mr.iid}`, href: mr.url }, ...info.filter(Boolean).map((text) => ({ text: text as string }))] })
  }
  const pl = f.pipeline
  if (pl) {
    const jobs = (pl.failedJobs ?? []).map((j) => ({ text: j.name, href: j.url }))
    rows.push({ key: 'pipeline', parts: [{ text: `pipeline #${pl.id} ${pl.status}`, href: pl.url }, ...jobs] })
  }
  const run = f.run
  if (run) {
    const result = run.result?.replace(/([a-z])([A-Z])/g, '$1 $2').toLowerCase() // NeedsInput → needs input
    const parts: FactPart[] = [{ text: [`run ${run.number}`, result].filter(Boolean).join(' ') + (run.error ? `: ${run.error}` : '') }]
    if (run.noteURL) parts.push({ text: 'failure note', href: run.noteURL })
    parts.push({ text: 'log', to: { name: 'task', params: { ...project, task: run.task }, query: { tab: 'logs' } } })
    rows.push({ key: 'run', parts })
  }
  const s = f.saved
  const saved = [
    s?.sha ? `${n(s.lines ?? 0, 'line')} saved as WIP commit ${shortSHA(s.sha)}` : '',
    s?.uncommittedLines ? `${n(s.uncommittedLines, 'line')} left uncommitted` : '',
  ].filter(Boolean)
  if (saved.length) rows.push({ key: 'saved', parts: saved.map((text) => ({ text })) })
  if (f.blocked?.length) rows.push({ key: 'blocked', parts: [{ text: [f.iid, ...f.blocked].map((i) => `#${i}`).join(' → ') }] })
  return rows
}

// problemCause is the card's cause line, the run's cause (agent text: plain
// text). Without one (a milestone-wide problem, a run of an older release)
// the pause cards' reasons are the fallback.
export function problemCause(f: Facts | undefined): string | undefined {
  return f?.run?.cause || undefined
}

// hidesReason: the cause line replaces a needs-input pause's or hold's
// reason, the generic text; other reasons say something else and stay.
export function hidesReason(cause: string | undefined, kind: string | undefined): boolean {
  return !!cause && kind === 'needsInput'
}
