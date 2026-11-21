import type { TimelineRowView, TimelineSegmentView, TimelineView } from '../../src/generated/views'
import { now } from './clock'

// GET /api/v1/timeline?project=team-a/shop&milestone=14: three issues of
// the milestone, all merged before the checks' now (2026-11-10 12:00 UTC).
// #11 and #12 ran side by side in W1, #12 held for half an hour; #13 waited
// for both and ran after them in W2. Any other milestone has run nothing.

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
    issue(11, 'Add the card provider', 1, [seg('running', at(8), at(9)), seg('waiting', at(9), at(9, 40))], at(9, 40)),
    issue(
      12,
      'Add the wallet provider',
      1,
      [seg('running', at(8, 10), at(9, 10)), seg('held', at(9, 10), at(9, 40)), seg('waiting', at(9, 40), at(10))],
      at(10),
    ),
    issue(13, 'Pick the provider at checkout', 2, [seg('running', at(10, 10), at(11)), seg('waiting', at(11), at(11, 30))], at(11, 30)),
  ],
}

export function answerTimeline(q: URLSearchParams): TimelineView {
  if (q.get('project') === 'team-a/shop' && q.get('milestone') === '14') return shopTimeline
  return { history: true, from: now.toISOString(), to: now.toISOString(), now: now.toISOString(), truncated: false, rows: [] }
}
