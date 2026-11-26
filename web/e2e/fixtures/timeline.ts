import type { TimelineRowView, TimelineSegmentView, TimelineView } from '../../src/generated/views'
import { now } from './clock'

// GET /api/v1/timeline?project=team-a/shop&milestone=14: three issues of
// the milestone, all merged before the checks' now (2026-11-10 12:00 UTC).
// #11 and #12 ran side by side in W1: #11 with a review at the end of its
// run, then CI, then the wait for its merge turn; #12 held for half an
// hour. #13 waited for both and ran after them in W2, then CI. Any other
// milestone has run nothing.

const at = (h: number, m = 0) => new Date(Date.UTC(2026, 10, 10, h, m)).toISOString()
const seg = (kind: TimelineSegmentView['kind'], start: string, end: string): TimelineSegmentView => ({ kind, start, end })

function issue(iid: number, title: string, wave: number, segments: TimelineSegmentView[], mergedAt: string): TimelineRowView {
  return {
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    milestoneTitle: 'Payment providers',
    issueIID: iid,
    issueTitle: title,
    wave,
    segments,
    mergedAt,
    deskURL: `/projects/team-a/shop/milestones/14/issues/${iid}`,
  }
}

export const shopTimeline: TimelineView = {
  history: true,
  from: at(8),
  to: now.toISOString(),
  now: now.toISOString(),
  truncated: false,
  rows: [
    issue(
      11,
      'Add the card provider',
      1,
      [seg('running', at(8), at(8, 40)), seg('review', at(8, 40), at(8, 45)), seg('ci', at(8, 45), at(9)), seg('waiting', at(9), at(9, 40))],
      at(9, 40),
    ),
    issue(
      12,
      'Add the wallet provider',
      1,
      [seg('running', at(8, 10), at(9, 10)), seg('held', at(9, 10), at(9, 40)), seg('waiting', at(9, 40), at(10))],
      at(10),
    ),
    issue(
      13,
      'Pick the provider at checkout',
      2,
      [seg('running', at(10, 10), at(10, 50)), seg('ci', at(10, 50), at(11)), seg('waiting', at(11), at(11, 30))],
      at(11, 30),
    ),
  ],
}

// The Overview's window (?from=&to=): team-a/shop's three issues of %14
// plus #20 of %15, still running, alongside #13; team-b's long-named
// project ran #7 of %3 this morning and #4 three days ago (only in 7 d).
const longProject = 'a-project-with-a-rather-long-name.example'
const daysAgo = (d: number, h: number) => new Date(Date.UTC(2026, 10, 10 - d, h)).toISOString()

export const overviewTimelineRows: TimelineRowView[] = [
  ...shopTimeline.rows,
  {
    ...issue(20, 'Refund by wallet', 1, [seg('running', at(10, 30), at(11)), { kind: 'running', start: at(11, 20) }], ''),
    milestoneIID: 15,
    milestoneTitle: 'Refunds',
    mergedAt: undefined,
    deskURL: '/projects/team-a/shop/milestones/15/issues/20',
  },
  ...[
    { iid: 7, title: 'Index the catalogue', segments: [seg('running', at(8, 30), at(10)), seg('waiting', at(10), at(11))], mergedAt: at(11) },
    { iid: 4, title: 'Add the search box', segments: [seg('running', daysAgo(3, 9), daysAgo(3, 11))], mergedAt: daysAgo(3, 11) },
  ].map(
    (r): TimelineRowView => ({
      namespace: 'team-b',
      project: longProject,
      milestoneIID: 3,
      milestoneTitle: 'Search',
      issueIID: r.iid,
      issueTitle: r.title,
      wave: 1,
      segments: r.segments,
      mergedAt: r.mergedAt,
      deskURL: `/projects/team-b/${longProject}/milestones/3/issues/${r.iid}`,
    }),
  ),
]

export function answerTimeline(q: URLSearchParams): TimelineView {
  if (q.get('project') === 'team-a/shop' && q.get('milestone') === '14') return shopTimeline
  const from = q.get('from')
  const to = q.get('to')
  if (from && to && !q.get('milestone')) return { history: true, from, to, now: now.toISOString(), truncated: false, rows: overviewTimelineRows }
  return { history: true, from: now.toISOString(), to: now.toISOString(), now: now.toISOString(), truncated: false, rows: [] }
}
