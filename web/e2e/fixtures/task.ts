import type {
  ChatPage,
  GitLabStatus,
  PromptView,
  Stage,
  Task,
  TaskResponse,
  TaskStatus,
  ThreadPart,
} from '../../src/types'
import type { SearchResponse, SearchResult } from '../../src/generated/views'
import { ago, cost } from './clock'
import { projects } from './rail'

// The tasks of team-a/shop and the task page's API. The task page's case is
// shop-281-implement: an Implement task whose last run asked a question,
// with a summary, prompts, a plan, CI, conditions and calls. Text written by
// agents and people carries markdown (bold, lists, links) for the markdown
// slices.

export const shop = projects[0]!
export const shopURL = shop.gitlab.webURL

// implementTask is an Implement task of shop's issue iid in a milestone.
export function implementTask(
  iid: number,
  title: string,
  milestone: { iid: number; title: string },
  stage: Stage,
  status: TaskStatus = {},
): Task {
  return {
    namespace: 'team-a',
    name: `shop-${iid}-implement`,
    project: 'shop',
    type: 'Implement',
    milestone: { id: 9000 + milestone.iid, iid: milestone.iid, title: milestone.title },
    issue: { id: 7000 + iid, iid, title, webURL: `${shopURL}/-/issues/${iid}` },
    suspend: false,
    autopilot: true,
    runRequest: 1,
    runPending: false,
    stage,
    createdAt: ago(60 * 24 * 4),
    status,
  }
}

export const payments = { iid: 14, title: 'Payment providers: refunds, partial captures and the provider switch' }
export const checkout = { iid: 12, title: 'Checkout without account' }

export const refundsTitle = 'Support refunds through the payment provider'
export const refundsTask = 'shop-281-implement'

// refundsSummary is the last run's summary of #281.
export const refundsSummary = `Implemented **full refunds** through the provider's refund API; **partial refunds** wait for an answer.

- Added \`RefundService\` with the provider client and a retry on \`429\`
- Stored each refund with its provider reference, see [the provider's refund docs](https://docs.example.com/payments/refunds)
- Tests for the full refund and for a failed provider call

**Open:** the issue does not say whether a partial refund may exceed the captured amount of one capture.`

// refundsPrompt is #281's agent question to the owners (open).
export const refundsPrompt: PromptView = {
  id: 'p-281-1',
  kind: 'needsInput',
  question:
    'Should a **partial refund** be split across captures when it exceeds the amount of a single capture?',
  context: `The provider allows refunds per capture only. Two ways out:

- **Split** the refund over the newest captures first
- **Refuse** refunds above one capture and ask for a second refund

See the [refund API](https://docs.example.com/payments/refunds#partial).`,
  options: [
    {
      id: 'split',
      label: 'Split across captures',
      detail: 'Refund the **newest captures first** until the amount is covered.',
      actions: ['Answers the agent', 'Starts a new run of #281'],
      recommended: true,
      fromAnswer: false,
    },
    {
      id: 'refuse',
      label: 'Refuse above one capture',
      detail: 'Show an error and ask for a second refund.',
      actions: ['Answers the agent', 'Starts a new run of #281'],
      recommended: false,
      fromAnswer: false,
    },
  ],
  allowFreeText: true,
  state: 'open',
  issueIID: 281,
  mrIID: 305,
  task: refundsTask,
  postedAt: ago(25),
  canAnswer: true,
  target: { namespace: 'team-a', task: refundsTask },
}

// refundsAnswered is an earlier, answered question of #281.
const refundsAnswered: PromptView = {
  id: 'p-281-0',
  kind: 'needsInput',
  question: 'Which **currency** do refunds use when the order was paid in a foreign currency?',
  options: [
    { id: 'order', label: 'The order currency', actions: ['Answers the agent'], recommended: true, fromAnswer: false },
    { id: 'shop', label: 'The shop currency', actions: ['Answers the agent'], recommended: false, fromAnswer: false },
  ],
  allowFreeText: false,
  state: 'answered',
  answer: {
    optionID: 'order',
    by: 'jo',
    at: ago(60 * 20),
    via: 'desk',
    outcome: 'Answered the agent and started **run 2** of #281.',
  },
  issueIID: 281,
  task: refundsTask,
  postedAt: ago(60 * 22),
  closedAt: ago(60 * 20),
  canAnswer: false,
  target: { namespace: 'team-a', task: refundsTask },
}

// refunds is the task of #281 as the task page and the milestone get it.
export const refunds: Task = implementTask(281, refundsTitle, payments, 'needs-input', {
  phase: 'Idle',
  workspaceClaimName: 'shop-281-implement-ws',
  run: {
    activeRunRequest: 2,
    observedRunRequest: 2,
    runCount: 2,
    lastActivityTime: ago(26),
    lastResult: 'NeedsInput',
    currentStep: 'Waiting for an answer on refunds of orders paid in a foreign currency',
    summary: refundsSummary,
    questions: [{ question: refundsPrompt.question, allowFreeText: true }],
    needsInputCause: 'agentQuestion',
    costUSD: '3.42',
    branch: 'agent/281-support-refunds-through-the-payment-provider',
    mergeRequestIID: 305,
    mergeRequestURL: `${shopURL}/-/merge_requests/305`,
    pushedSHA: '4f1c9e2a7b3d5f60812a4c6e8b0d2f4a6c8e0b1d',
    pushedAt: ago(30),
    ciRound: 1,
    blockingFindings: 0,
    reviewErrors: 1,
    reviewedSHA: '9a8b7c6d5e4f30211a2b3c4d5e6f708192a3b4c5',
    calls: [
      {
        seq: 1,
        at: ago(60 * 21),
        kind: 'implement',
        role: 'implementer',
        engine: 'claude',
        model: 'claude-opus-5-5',
        inputTokens: 182_000,
        outputTokens: 14_300,
        cacheReadTokens: 1_420_000,
        costUSD: '2.10',
        costSource: 'reported',
        turns: 41,
        durationMS: 612_000,
        run: 1,
      },
      {
        seq: 2,
        at: ago(40),
        kind: 'implement',
        role: 'implementer',
        engine: 'claude',
        model: 'claude-opus-5-5',
        inputTokens: 96_000,
        outputTokens: 8_100,
        cacheReadTokens: 880_000,
        costUSD: '1.32',
        costSource: 'reported',
        turns: 23,
        durationMS: 338_000,
        run: 2,
      },
    ],
  },
  ci: {
    sha: '4f1c9e2a7b3d5f60812a4c6e8b0d2f4a6c8e0b1d',
    state: 'Failed',
    pipelineID: 88214,
    pipelineURL: `${shopURL}/-/pipelines/88214`,
    retried: true,
    failedJobs: ['test:integration-payments', 'lint'],
  },
  plan: [
    { key: 'refund-service', iid: 281, title: refundsTitle },
    { key: 'partial-captures', iid: 282, title: 'Partial captures for split shipments', dependsOn: [281] },
    { key: 'provider-switch', iid: 283, title: 'Switch the payment provider per country', dependsOn: [281, 282] },
  ],
  conditions: [
    {
      type: 'Ready',
      status: 'True',
      lastTransitionTime: ago(26),
      reason: 'NeedsInput',
      message: 'The agent asked 1 question.',
    },
    {
      type: 'Blocked',
      status: 'False',
      lastTransitionTime: ago(60 * 24),
      reason: 'WithinLimits',
      message: 'Runs, CI rounds and spend are within the project limits.',
    },
    {
      type: 'Reviewed',
      status: 'Unknown',
      lastTransitionTime: ago(30),
      reason: 'ReviewerFailed',
      message: 'The security reviewer failed: rate limited by the engine until 12:40.',
    },
  ],
})

// refundsResponse is GET /projects/team-a/shop/tasks/shop-281-implement.
export const refundsResponse: TaskResponse = {
  project: shop,
  task: refunds,
  prompts: [refundsPrompt, refundsAnswered],
  cost: cost('3.42', 2_580_000),
  permissions: { transcript: true, gitlab: true, steer: true, chat: true },
}

// GET …/tasks/shop-281-implement/chat: the Desk chat, newest first.
export const refundsChat: ChatPage = {
  messages: [
    {
      id: 412,
      at: ago(26),
      role: 'agent',
      kind: 'message',
      authorName: 'agent',
      body: `I stopped after the full refunds: **partial refunds** need a decision.

- The provider refunds per capture
- An order can have up to 3 captures

I posted the question on the task page.`,
      replyTo: 411,
    },
    {
      id: 411,
      at: ago(50),
      role: 'user',
      kind: 'message',
      author: 'jo',
      authorName: 'Jo',
      body: 'Please keep the refund reasons in the **order history**, see [#270](https://gitlab.example.com/team-a/shop/-/issues/270).',
      state: 'answered',
    },
  ],
}

// GET /gitlab: GitLab is connected for the signed-in user.
export const gitlabStatus: GitLabStatus = {
  enabled: true,
  url: 'https://gitlab.example.com',
  connected: true,
  username: 'jo',
}

// GET …/tasks/shop-281-implement/thread: the issue's and the MR's comments.
export const refundsThread: { parts: ThreadPart[] } = {
  parts: [
    {
      target: 'issue',
      iid: 281,
      webURL: `${shopURL}/-/issues/281`,
      notes: [
        {
          id: 51001,
          author: 'jo',
          body: `Refunds must work for **card and wallet** payments.

- full refunds first
- partial refunds after #282

Provider docs: [refunds](https://docs.example.com/payments/refunds)`,
          createdAt: ago(60 * 24 * 3),
        },
        { id: 51002, author: 'ai-operator', body: 'Started working on this in !305.', system: true, createdAt: ago(60 * 21) },
      ],
    },
    {
      target: 'mr',
      iid: 305,
      webURL: `${shopURL}/-/merge_requests/305`,
      notes: [
        {
          id: 51010,
          author: 'reviewer',
          body: '**Looks good.** One nit: name the retry constant after the provider limit, not `3`.',
          createdAt: ago(32),
        },
      ],
    },
  ],
}

// GET …/tasks/shop-m14-pilot/chat: the milestone pilot's Desk chat.
export const pilotChat: ChatPage = {
  messages: [
    {
      id: 520,
      at: ago(60 * 3),
      role: 'agent',
      kind: 'message',
      authorName: 'pilot',
      body: `#283 is **two changes** in one issue:

1. the provider switch per country
2. its admin setting

I proposed to split it, see [the proposal](https://gitlab.example.com/team-a/shop/-/issues/278#note_51180).`,
      replyTo: 519,
    },
    {
      id: 519,
      at: ago(60 * 3 + 5),
      role: 'user',
      kind: 'message',
      author: 'jo',
      authorName: 'Jo',
      body: 'Is #283 small enough for **one** merge request?',
      state: 'answered',
    },
  ],
}

// GET /search?q=: #281 (with its merge request !298) matches "!298", "#281"
// and words of its title; other terms find nothing.
export function answerSearch(q: URLSearchParams): SearchResponse {
  const term = (q.get('q') ?? '').toLowerCase()
  const hit = term === '!298' || term === '#281' || refundsTitle.toLowerCase().includes(term)
  const result: SearchResult = {
    namespace: 'team-a',
    project: 'shop',
    task: refundsTask,
    type: 'Implement',
    title: refundsTitle,
    issueIID: 281,
    mergeRequestIID: 298,
    createdAt: ago(60 * 24),
  }
  return { results: hit ? [result] : [] }
}
