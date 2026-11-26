// The shop's loose issues queue (LooseIssues.vue, R8 of "Loose issues"):
// the Queue task's items in every state and the open issues without a
// milestone, #350 and #421 among them. The shop's own detail keeps
// looseIssues off; loose-issues.spec.ts turns it on.
import { ago } from './clock'
import type { LooseIssue, LooseIssues, LooseItem } from '../../src/generated/views'

const web = 'https://gitlab.example.com/team-a/shop/-/'
const issueURL = (iid: number) => `${web}issues/${iid}`

const item = (iid: number, title: string, state: LooseItem['state'], extra: Partial<LooseItem> = {}): LooseItem => ({
  iid,
  title,
  webURL: issueURL(iid),
  state,
  ...extra,
})

export const looseItems: LooseItem[] = [
  item(128, 'Webhook retries log twice', 'Active', { task: 'shop-128-implement', headSHA: '4f2a9c1e8b7d', mergeRequestIID: 902, mergeRequestURL: `${web}merge_requests/902` }),
  item(46, 'Cache the GitLab user lookup', 'Active', {
    task: 'shop-46-implement',
    headSHA: '9b1c0d2e3f4a',
    holdReason: 'CI stays red after 3 fix rounds (job `unit`).',
    mergeRequestIID: 899,
    mergeRequestURL: `${web}merge_requests/899`,
  }),
  item(190, 'Typo in the install guide', 'Pending'),
  item(58, 'Flaky e2e: checkout timeout', 'Merged', { mergeRequestIID: 880, mergeRequestURL: `${web}merge_requests/880`, mergedAt: ago(95) }),
  item(677, 'Duplicate of #678', 'Left', { leftReason: 'dropped by @ada' }),
]

const issue = (iid: number, title: string, labels: string[], author: string, minutes: number, inQueue = false): LooseIssue => ({
  iid,
  title,
  labels,
  author,
  updatedAt: ago(minutes),
  webURL: issueURL(iid),
  inQueue,
})

export const looseOpenIssues: LooseIssue[] = [
  issue(421, 'Runner timeout in the e2e job', ['ci::flaky'], 'grace', 20),
  issue(420, 'Runner timeout in the lint job', ['ci::flaky'], 'grace', 45),
  issue(350, 'Flaky helm-lint', ['ci::flaky', 'agent::ready'], 'ai-operator', 180),
  issue(190, 'Typo in the install guide', ['docs'], 'linus', 600, true),
  issue(128, 'Webhook retries log twice', ['bug'], 'ada', 1440, true),
  issue(46, 'Cache the GitLab user lookup', [], 'ada', 2880, true),
]

export const looseIssues: LooseIssues = {
  queue: { phase: 'Running', maxParallel: 2 },
  items: looseItems,
  issues: looseOpenIssues,
  take: true,
  ownerSubjects: ['ada@example.com'],
  queueTask: 'shop-loose-issues',
}
