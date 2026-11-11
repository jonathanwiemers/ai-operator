import type {
  HistoryResponse,
  IssueView,
  MilestoneView,
  ProjectDetail,
  StreamEvent,
  StreamPage,
  Task,
} from '../../src/types'
import { ago, cost, now } from './clock'
import { stream as dashboardStream } from './overview'
import { checkout, implementTask, payments, refunds, refundsPrompt, refundsTitle, shop, shopURL } from './task'

// The project and milestone pages of team-a/shop: GET /projects/team-a/shop,
// …/history, …/milestones/{iid}, …/milestones/{iid}/issues/{issue} and
// /stream. Milestone %14 (Payment providers) is paused on #281's question and
// has release rows, prompts, companions, pauses and a retrospective; %12
// (Checkout without account) is running.

const milestoneURL = (iid: number) => `${shopURL}/-/milestones/${iid}`
const issueURL = (iid: number) => `${shopURL}/-/issues/${iid}`
const mrURL = (iid: number) => `${shopURL}/-/merge_requests/${iid}`

// specTask is the Spec task of a milestone, whose status holds its autopilot.
function specTask(m: { iid: number; title: string }, phase: 'Running' | 'Paused'): Task {
  return {
    namespace: 'team-a',
    name: `shop-m${m.iid}-spec`,
    project: 'shop',
    type: 'Spec',
    milestone: { id: 9000 + m.iid, iid: m.iid, title: m.title },
    suspend: false,
    autopilot: true,
    runRequest: 1,
    runPending: false,
    stage: 'planned',
    createdAt: ago(60 * 24 * 6),
    status: {
      phase: 'Idle',
      autopilot: {
        phase,
        trackingIssueIID: m.iid === payments.iid ? 278 : 270,
        ...(phase === 'Paused' ? { pauseKind: 'needsInput', pausedFor: 281, pausedReason: pausedReason } : {}),
      },
    },
  }
}

const pausedReason = `Issue #281 needs an answer: **partial refunds** across captures.

- Answer the question on the [task page](${issueURL(281)})
- or skip #281 and continue with #282`

// The implement tasks of %14 and %12.
const paymentsTasks: Task[] = [
  implementTask(280, 'Provider SDK 4 with refund support', payments, 'review', {
    phase: 'Idle',
    run: { runCount: 1, lastResult: 'Succeeded', mergeRequestIID: 303, mergeRequestURL: mrURL(303) },
    ci: { sha: 'a1b2c3d4e5f6', state: 'Success', pipelineID: 88190, pipelineURL: `${shopURL}/-/pipelines/88190` },
  }),
  refunds,
  implementTask(282, 'Partial captures for split shipments', payments, 'pending'),
]
const checkoutTasks: Task[] = [
  implementTask(276, 'Guest checkout: address form', checkout, 'review', {
    phase: 'Idle',
    run: { runCount: 2, lastResult: 'Succeeded', mergeRequestIID: 298, mergeRequestURL: mrURL(298) },
  }),
  implementTask(277, 'Guest checkout: keep the cart after the address step', checkout, 'working', {
    phase: 'Running',
    podName: 'shop-277-implement-run-3',
    run: { runCount: 3, currentStep: 'review', mergeRequestIID: 301, mergeRequestURL: mrURL(301) },
  }),
  implementTask(278, 'Guest checkout: confirmation mail', checkout, 'pending'),
]

// GET /projects/team-a/shop: the project with its milestones, newest first.
export const shopDetail: ProjectDetail = {
  ...shop,
  milestones: [
    {
      ...cost('9.85', 1_870_000),
      id: 9014,
      iid: payments.iid,
      title: payments.title,
      webURL: milestoneURL(payments.iid),
      spec: specTask(payments, 'Paused'),
      trackingIssueIID: 278,
      tasks: paymentsTasks,
    },
    {
      ...cost('21.10', 4_050_000),
      id: 9012,
      iid: checkout.iid,
      title: checkout.title,
      webURL: milestoneURL(checkout.iid),
      spec: specTask(checkout, 'Running'),
      trackingIssueIID: 270,
      tasks: checkoutTasks,
    },
  ],
  unplanned: [
    {
      ...implementTask(290, 'Fix the typo in the footer', { iid: 0, title: '' }, 'ci', { phase: 'Idle' }),
      milestone: undefined,
      autopilot: false,
    },
  ],
}

// GET /projects/team-a/shop/history?days=30&tz=…: activity of the last 30
// days, ending today.
export const shopHistory: HistoryResponse = (() => {
  const days = Array.from({ length: 30 }, (_, i) => {
    const d = new Date(now.getTime() - (29 - i) * 86_400_000)
    const events = (i * 7) % 11
    return {
      day: d.toISOString().slice(0, 10),
      runs: events % 4,
      merged: i % 5 === 0 ? 1 : 0,
      costUSD: Math.round(events * 37) / 100,
      events,
    }
  })
  const sum = (k: 'runs' | 'merged' | 'costUSD' | 'events') => days.reduce((a, d) => a + d[k], 0)
  return {
    days,
    totals: { runs: sum('runs'), merged: sum('merged'), costUSD: sum('costUSD'), events: sum('events') },
    previous: { runs: 31, merged: 5, costUSD: 52.4, events: 140 },
  }
})()

const figures = (usd: string, tokens: number, wallMin: number, agentMin: number, pausedMin: number) => ({
  ...cost(usd, tokens),
  wallSec: wallMin * 60,
  agentSec: agentMin * 60,
  pausedSec: pausedMin * 60,
})

// GET /projects/team-a/shop/milestones/14: paused on #281's question.
export const paymentsMilestone: MilestoneView = {
  ...cost('9.85', 1_870_000),
  id: 9014,
  iid: payments.iid,
  title: payments.title,
  webURL: milestoneURL(payments.iid),
  state: 'paused',
  spec: specTask(payments, 'Paused'),
  issues: [
    {
      iid: 279,
      title: 'Refund reasons in the order history',
      state: 'merged',
      mergeRequestIID: 302,
      mergeRequestURL: mrURL(302),
      figures: figures('2.30', 410_000, 95, 31, 0),
      version: '2026.11.3',
    },
    {
      iid: 280,
      title: 'Provider SDK 4 with refund support',
      state: 'merged',
      mergeRequestIID: 303,
      mergeRequestURL: mrURL(303),
      task: 'shop-280-implement',
      figures: figures('4.13', 820_000, 260, 54, 42),
    },
    {
      iid: 281,
      title: refundsTitle,
      state: 'active',
      mergeRequestIID: 305,
      mergeRequestURL: mrURL(305),
      task: refunds.name,
      figures: figures('3.42', 640_000, 1290, 16, 24),
    },
    { iid: 282, title: 'Partial captures for split shipments', state: 'pending', task: 'shop-282-implement' },
    { iid: 283, title: 'Switch the payment provider per country', state: 'pending' },
    { iid: 284, title: 'Document the refund flow for support', state: 'planned' },
  ],
  releases: [
    {
      id: 1,
      by: 'planner',
      why: 'Refund reasons are **visible to support** right away; nothing else depends on them.',
      afterIssue: 279,
      sha: 'c0ffee1234567890',
      releasedSHA: 'c0ffee1234567890',
      state: 'Released',
      issues: [279],
      mergeRequests: [302],
      version: '2026.11.3',
      pipelineURL: `${shopURL}/-/pipelines/88150`,
      releaseURL: `${shopURL}/-/releases/2026.11.3`,
      decidedAt: ago(60 * 30),
      startedAt: ago(60 * 29),
      endedAt: ago(60 * 28),
    },
    {
      id: 2,
      by: 'pilot',
      actor: 'pilot',
      why: `The SDK upgrade must be live before #281:

- the companion in **team-a/payments-sdk** lands first
- see [the SDK changelog](https://docs.example.com/payments/sdk/4)`,
      afterIssue: 280,
      state: 'Failed',
      reason: 'The release pipeline failed in `publish:image` (registry timeout).',
      issues: [280],
      mergeRequests: [303],
      pipelineURL: `${shopURL}/-/pipelines/88201`,
      decidedAt: ago(60 * 6),
      startedAt: ago(60 * 6),
      endedAt: ago(60 * 5),
    },
    {
      id: 3,
      by: 'planner',
      why: 'Refunds and partial captures ship together.',
      afterIssue: 282,
      state: 'Planned',
      issues: [281, 282],
      mergeRequests: [],
      decidedAt: ago(60 * 24 * 5),
    },
  ],
  autopilot: {
    phase: 'Paused',
    pausedReason,
    pauseKind: 'needsInput',
    pausedFor: 281,
    head: '4f1c9e2a7b3d5f60812a4c6e8b0d2f4a6c8e0b1d',
    exits: [
      {
        id: 'answer',
        prompt: true,
        does: 'Answer the question; the agent starts a new run of #281.',
        status: 'waiting',
        allowed: true,
      },
      {
        id: 'skip',
        command: 'skip',
        does: 'Skip #281 and continue with #282.',
        allowed: true,
      },
      {
        id: 'resume',
        auto: true,
        when: 'a later run of #281 is reviewed and green',
        does: 'Autopilot continues by itself.',
        status: 'waiting',
        detail: 'Run 2 asked a question.',
      },
    ],
    items: [
      { iid: 279, title: 'Refund reasons in the order history', state: 'Merged', mergeRequestIID: 302, mergeRequestURL: mrURL(302) },
      { iid: 280, title: 'Provider SDK 4 with refund support', state: 'Merged', mergeRequestIID: 303, mergeRequestURL: mrURL(303), task: 'shop-280-implement' },
      { iid: 281, title: refundsTitle, state: 'Active', mergeRequestIID: 305, mergeRequestURL: mrURL(305), task: refunds.name },
      { iid: 282, title: 'Partial captures for split shipments', state: 'Pending', task: 'shop-282-implement' },
      { iid: 283, title: 'Switch the payment provider per country', state: 'Pending' },
    ],
  },
  tracking: { iid: 278, webURL: issueURL(278) },
  pilot: {
    task: 'shop-m14-pilot',
    autonomy: 'managed',
    proposals: [
      {
        id: 7,
        summary: `Split #283 into **two issues**:

- the provider switch per country
- the admin setting for it`,
        postedAt: ago(60 * 3),
      },
    ],
    costUSD: '0.84',
    phase: 'Idle',
  },
  prompts: [
    refundsPrompt,
    {
      id: 'Q3',
      kind: 'assessment',
      question: 'Release **SDK 4** before #281, or ship it together with refunds?',
      context: 'Releasing first lets the [payments-sdk companion](https://gitlab.example.com/team-a/payments-sdk/-/merge_requests/41) land.',
      options: [
        { id: 'first', label: 'Release SDK 4 first', actions: ['Plans a release after #280'], recommended: true, fromAnswer: false },
        { id: 'together', label: 'Ship together', actions: ['Plans one release after #282'], recommended: false, fromAnswer: false },
      ],
      allowFreeText: false,
      state: 'answered',
      answer: { optionID: 'first', by: 'jo', at: ago(60 * 7), via: 'desk', outcome: 'Planned **release 2** after #280.' },
      postedAt: ago(60 * 8),
      closedAt: ago(60 * 7),
      canAnswer: false,
      target: { namespace: 'team-a', task: 'shop-m14-pilot' },
    },
  ],
  tasks: paymentsTasks,
  companions: [
    {
      homeIssueIID: 280,
      project: 'team-a/payments-sdk',
      mrIID: 41,
      mrURL: 'https://gitlab.example.com/team-a/payments-sdk/-/merge_requests/41',
      state: 'waitingRelease',
      chip: 'waiting for release',
      reason: 'Waits for **release 2** of team-a/shop, whose pipeline failed.',
      autoMerge: true,
    },
    {
      homeIssueIID: 281,
      project: 'team-a/payments-sdk',
      mrIID: 44,
      mrURL: 'https://gitlab.example.com/team-a/payments-sdk/-/merge_requests/44',
      chip: 'draft',
    },
  ],
  pauses: {
    pauses: [
      { kind: 'needsInput', reason: 'Issue #281 needs an answer.', item: 281, since: ago(24), seconds: 24 * 60 },
      {
        kind: 'ciRed',
        reason: 'The pipeline of #280 failed twice in `test:integration-payments`.',
        item: 280,
        since: ago(60 * 26),
        until: ago(60 * 25),
        resolvedBy: 'selfHealed',
        via: 'push',
        seconds: 42 * 60,
      },
    ],
    totals: {
      count: 2,
      pausedSec: 66 * 60,
      byKind: { needsInput: { count: 1, sec: 24 * 60 }, ciRed: { count: 1, sec: 42 * 60 } },
      meanResumeSec: 42 * 60,
      byResolver: { selfHealed: 1 },
      byVia: { push: 1 },
    },
  },
  retrospective: {
    text: `## What went well

- **Refund reasons** shipped in 2026.11.3 within a day
- The SDK companion was drafted with #280

## What did not

- The release pipeline of **release 2** failed on a registry timeout
- #281's issue left partial refunds open, see [the question](${issueURL(281)})`,
    postedAt: ago(60 * 2),
    noteURL: `${issueURL(278)}#note_51200`,
    followUps: [
      {
        number: 1,
        title: 'Retry the registry push in the release job',
        body: 'The **publish:image** job should retry on `502` and timeouts.',
        state: 'open',
      },
      {
        number: 2,
        title: 'Say in the issue template whether partial amounts are allowed',
        state: 'issue',
        targetIID: 291,
        targetURL: issueURL(291),
      },
    ],
    costDrivers: [
      { label: '#280', kind: 'issue', costUSD: '4.13', costKnown: true, tokens: 820_000 },
      { label: '#281', kind: 'issue', costUSD: '3.42', costKnown: true, tokens: 640_000 },
      { label: 'implementer', kind: 'role', costUSD: '6.90', costKnown: true, tokens: 1_310_000 },
    ],
  },
  permissions: {
    gitlab: true,
    steer: true,
    chat: true,
    transcript: true,
    merge: true,
    draft: true,
    followUp: true,
    dismiss: true,
  },
}

// GET /projects/team-a/shop/milestones/12: running, what the project page
// loads for its context panel.
export const checkoutMilestone: MilestoneView = {
  ...cost('21.10', 4_050_000),
  id: 9012,
  iid: checkout.iid,
  title: checkout.title,
  webURL: milestoneURL(checkout.iid),
  state: 'running',
  spec: specTask(checkout, 'Running'),
  issues: [
    { iid: 275, title: 'Guest checkout: session without account', state: 'merged', mergeRequestIID: 296, mergeRequestURL: mrURL(296) },
    { iid: 276, title: 'Guest checkout: address form', state: 'merged', mergeRequestIID: 298, mergeRequestURL: mrURL(298) },
    { iid: 277, title: 'Guest checkout: keep the cart after the address step', state: 'active', task: 'shop-277-implement' },
    { iid: 278, title: 'Guest checkout: confirmation mail', state: 'pending', task: 'shop-278-implement' },
  ],
  releases: [],
  autopilot: {
    phase: 'Running',
    items: [
      { iid: 275, title: 'Guest checkout: session without account', state: 'Merged' },
      { iid: 276, title: 'Guest checkout: address form', state: 'Merged' },
      { iid: 277, title: 'Guest checkout: keep the cart after the address step', state: 'Active', task: 'shop-277-implement' },
      { iid: 278, title: 'Guest checkout: confirmation mail', state: 'Pending', task: 'shop-278-implement' },
    ],
  },
  tracking: { iid: 270, webURL: issueURL(270) },
  pilot: { task: 'shop-m12-pilot', autonomy: 'managed', proposals: [], costUSD: '0.31', phase: 'Idle' },
  prompts: [],
  tasks: checkoutTasks,
  // One companion, so the project page's context panel shows all four parts.
  companions: [
    {
      homeIssueIID: 277,
      project: 'team-a/checkout-widget',
      mrIID: 12,
      mrURL: 'https://gitlab.example.com/team-a/checkout-widget/-/merge_requests/12',
      chip: 'draft',
    },
  ],
  permissions: {
    gitlab: true,
    steer: true,
    chat: true,
    transcript: true,
    merge: true,
    draft: false,
    followUp: false,
    dismiss: false,
  },
}

export const milestones: Record<string, MilestoneView> = {
  [payments.iid]: paymentsMilestone,
  [checkout.iid]: checkoutMilestone,
}

// GET /projects/team-a/shop/milestones/14/issues/281.
export const refundsIssue: IssueView = {
  ...paymentsMilestone.issues[2]!,
  namespace: 'team-a',
  project: 'shop',
  milestoneIID: payments.iid,
  milestoneTitle: payments.title,
  webURL: issueURL(281),
}

// The events of %14, newest first, on top of the dashboard's.
const paymentsEvents: StreamEvent[] = [
  {
    id: 2010,
    at: ago(26),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    issueIID: 281,
    task: refunds.name,
    kind: 'run.finished',
    summary: 'Run 2 needs input',
    data: { result: 'needsInput', runSummary: refunds.status.run!.summary, costUSD: '1.32', durationSec: 338, runCount: 2 },
  },
  {
    id: 2009,
    at: ago(30),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    issueIID: 281,
    mergeRequestIID: 305,
    task: refunds.name,
    kind: 'ci',
    summary: 'Pipeline failed: test:integration-payments, lint',
    data: { state: 'failed', pipelineID: 88214, pipelineURL: `${shopURL}/-/pipelines/88214`, failedJobs: ['test:integration-payments', 'lint'] },
  },
  {
    id: 2008,
    at: ago(60 * 5),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    kind: 'release.ended',
    summary: 'Release 2 failed',
    data: { releaseID: 2, state: 'Failed', reason: 'The release pipeline failed in `publish:image` (registry timeout).', pipelineURL: `${shopURL}/-/pipelines/88201` },
  },
  {
    id: 2007,
    at: ago(60 * 6),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    kind: 'release.decided',
    actor: 'pilot',
    summary: 'Release 2 after #280',
    data: { releaseID: 2, afterIssue: 280, why: 'The SDK upgrade must be live before #281.', issues: [280], mergeRequests: [303] },
  },
  {
    id: 2006,
    at: ago(60 * 7),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    kind: 'prompt.answered',
    actor: 'jo',
    summary: 'Release SDK 4 before #281: Release SDK 4 first',
    data: { promptID: 'Q3', promptKind: 'assessment', optionLabel: 'Release SDK 4 first', by: 'jo' },
  },
  {
    id: 2005,
    at: ago(60 * 9),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    issueIID: 281,
    kind: 'note',
    actor: 'jo',
    summary: 'Refunds must work for **card and wallet** payments.',
    data: {
      body: 'Refunds must work for **card and wallet** payments.\n\n- full refunds first\n- partial refunds after #282\n\nProvider docs: [refunds](https://docs.example.com/payments/refunds)',
      noteable: 'issue',
    },
  },
  // Three earlier notes on #281: the thread folds the first behind "1 earlier".
  {
    id: 2004,
    at: ago(60 * 10),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    issueIID: 281,
    kind: 'note',
    actor: 'ai-operator',
    summary: 'Queued #281 after #280.',
    data: { body: 'Queued #281 after #280, since it needs **SDK 4**.', noteable: 'issue', bot: true },
  },
  {
    id: 2003,
    at: ago(60 * 11),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    issueIID: 281,
    kind: 'note',
    actor: 'reviewer',
    summary: 'Which provider first?',
    data: { body: 'Which provider first?', noteable: 'issue' },
  },
  {
    id: 2002,
    at: ago(60 * 12),
    namespace: 'team-a',
    project: 'shop',
    milestoneIID: 14,
    issueIID: 281,
    kind: 'note',
    actor: 'jo',
    summary: 'Opened for the refunds work.',
    data: { body: 'Opened for the refunds work.', noteable: 'issue' },
  },
]

const allEvents = [...paymentsEvents, ...dashboardStream.events].sort((a, b) => b.id - a.id)

// answerStream is GET /stream: the events of the query's project,
// milestone and issue; nothing newer for ?after=.
export function answerStream(q: URLSearchParams): StreamPage {
  if (q.has('after')) return { events: [], newest: `c-${allEvents[0]!.id}`, history: true }
  const project = q.get('project')
  const milestone = q.get('milestone')
  const issue = q.get('issue')
  const events = allEvents.filter(
    (e) =>
      (!project || `${e.namespace}/${e.project}` === project) &&
      (!milestone || String(e.milestoneIID) === milestone) &&
      (!issue || String(e.issueIID) === issue),
  )
  // The dashboard keeps its own page (older cursor and all).
  if (!project) return dashboardStream
  return { events, newest: events[0] ? `c-${events[0].id}` : undefined, history: true }
}
