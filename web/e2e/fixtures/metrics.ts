import type { MetricsPausesView, PauseBucketView } from '../../src/generated/views'
import { now } from './clock'

// GET /api/v1/metrics/pauses?range=&from=&to=&project=&tz=: the milestone's
// scenario over the last 30 days up to the checks' now (2026-11-10 12:00
// UTC). Milestone 14 of team-a/shop paused twice for red CI (2 h and 30 min,
// both resumed by the owner) and is paused now for a budget stop, since
// 09:00; the long-named project paused once for review findings. With
// range=90d the answer says truncated, so the filtered route shows the note.

const shop = { namespace: 'team-a', project: 'shop' }
const long = { namespace: 'team-b', project: 'a-project-with-a-rather-long-name.example' }
const pilot = (ns: string, p: string, iid: number) => `/projects/${ns}/${p}/milestones/${iid}?tab=pilot`

const days = Array.from({ length: 30 }, (_, i) => new Date(Date.UTC(2026, 9, 12 + i)).toISOString().slice(0, 10))

const budgetSec = (now.getTime() - Date.parse('2026-11-10T09:00:00Z')) / 1000

// The pauses that started on a day: [kind, seconds].
const started: Record<string, [string, number][]> = {
  '2026-10-20': [['ciRed', 7200]],
  '2026-10-28': [['findings', 2700]],
  '2026-11-03': [['ciRed', 1800]],
  '2026-11-10': [['budget', budgetSec]],
}

const buckets: PauseBucketView[] = days.map((bucket) => {
  const ps = started[bucket] ?? []
  const byKind: PauseBucketView['byKind'] = {}
  for (const [kind, sec] of ps) {
    const k = (byKind[kind] ??= { count: 0, sec: 0 })
    k.count++
    k.sec += sec
  }
  return { bucket, count: ps.length, pausedSec: ps.reduce((a, [, s]) => a + s, 0), byKind }
})

const pauses: MetricsPausesView = {
  range: '30d',
  from: days[0]!,
  to: days[days.length - 1]!,
  bucket: 'day',
  tz: 'UTC',
  buckets,
  totals: {
    count: 4,
    pausedSec: 7200 + 1800 + 2700 + budgetSec,
    byKind: {
      ciRed: { count: 2, sec: 9000 },
      budget: { count: 1, sec: budgetSec },
      findings: { count: 1, sec: 2700 },
    },
    meanResumeSec: (7200 + 1800 + 2700) / 3,
    byResolver: { owner: 2, selfHealed: 1 },
    byVia: { command: 2, push: 1 },
  },
  openNow: 1,
  byProject: [
    { ...shop, count: 3, pausedSec: 9000 + budgetSec },
    { ...long, count: 1, pausedSec: 2700 },
  ],
  longest: [
    { ...shop, milestoneIID: 14, kind: 'budget', reason: 'The milestone budget of $40 is used ($41.20 spent).', seconds: budgetSec, since: '2026-11-10T09:00:00Z', open: true, deskURL: pilot('team-a', 'shop', 14) },
    { ...shop, milestoneIID: 14, kind: 'ciRed', reason: 'CI of !735 is red: unit tests failed', seconds: 7200, since: '2026-10-20T08:00:00Z', open: false, deskURL: pilot('team-a', 'shop', 14) },
    { ...long, milestoneIID: 3, kind: 'findings', reason: 'Two review findings are open on !12', seconds: 2700, since: '2026-10-28T13:00:00Z', open: false, deskURL: pilot(long.namespace, long.project, 3) },
    { ...shop, milestoneIID: 14, kind: 'ciRed', seconds: 1800, since: '2026-11-03T10:00:00Z', open: false, deskURL: pilot('team-a', 'shop', 14) },
  ],
  truncated: false,
}

// metricsPauses answers the query: one project's pauses with ?project=
// (the long-named project's milestone left out), truncated with range=90d.
export function metricsPauses(q: URLSearchParams): MetricsPausesView {
  const truncated = q.get('range') === '90d'
  if (q.get('project') !== 'team-a/shop') return { ...pauses, truncated }
  const byKind = { ciRed: pauses.totals.byKind.ciRed!, budget: pauses.totals.byKind.budget! }
  return {
    ...pauses,
    truncated,
    buckets: buckets.map((b) => (b.byKind.findings ? { ...b, count: 0, pausedSec: 0, byKind: {} } : b)),
    totals: {
      count: 3,
      pausedSec: 9000 + budgetSec,
      byKind,
      meanResumeSec: 4500,
      byResolver: { owner: 2 },
      byVia: { command: 2 },
    },
    byProject: [pauses.byProject[0]!],
    longest: pauses.longest.filter((p) => p.project === 'shop'),
  }
}
