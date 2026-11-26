// The shop's loose issues queue (LooseIssues.vue, R8 of "Loose issues"):
// the Queue task's items in every state and the open issues without a
// milestone, #350 and #421 among them. The shop's own detail keeps
// looseIssues off; loose-issues.spec.ts turns it on.
import { ago } from './clock'
import type { LooseIssue, LooseIssues, LooseItem } from '../../src/generated/views'
import type { Facts } from '../../src/generated/problem'

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
  problems: [],
  prompts: [],
  proposals: [],
}

// loosePilot is the queue with its pilot (R6 of "a pilot for the queue"):
// #421 held for red CI, with its problem facts and a question to the
// owners, a proposal of the pilot, and round 2 of five issues.
const facts421: Facts = {
  iid: 421,
  issue: { iid: 421, title: 'Runner timeout in the e2e job', url: issueURL(421) },
  mergeRequest: { iid: 905, url: `${web}merge_requests/905` },
  pipeline: { id: 7712, url: `${web}pipelines/7712`, status: 'failed', failedJobs: [{ name: 'e2e', url: `${web}jobs/9911` }] },
}

export const loosePilot: LooseIssues = {
  ...looseIssues,
  items: [
    ...looseItems,
    item(421, 'Runner timeout in the e2e job', 'Active', {
      task: 'shop-421-implement',
      headSHA: '7c3e1a2b4d5f',
      holdReason: 'CI stays red after 3 fix rounds (job `e2e`).',
      mergeRequestIID: 905,
      mergeRequestURL: `${web}merge_requests/905`,
    }),
  ],
  issues: looseOpenIssues.map((i) => (i.iid === 421 ? { ...i, inQueue: true } : i)),
  problems: [facts421],
  prompts: [
    {
      id: 'Q9',
      kind: 'assessment',
      question: 'Retry the `e2e` job of #421 once more, or drop the issue?',
      options: [
        { id: 'retry', label: 'Retry the job', actions: ['Retries job e2e'], recommended: true, fromAnswer: false },
        { id: 'drop', label: 'Drop #421', actions: ['Drops #421 from the queue'], recommended: false, fromAnswer: false },
      ],
      allowFreeText: true,
      state: 'open',
      issueIID: 421,
      postedAt: ago(12),
      canAnswer: true,
      target: { namespace: 'team-a', task: 'shop-loose-pilot' },
    },
  ],
  proposals: [{ id: 3, summary: 'Rebase !905 onto main: the e2e image changed there.', postedAt: ago(8) }],
  pilotTask: 'shop-loose-pilot',
  round: { number: 2, startedAt: ago(150), finished: 1, roundIssues: 5, roundHours: 24, costUSD: '0.85' },
}
