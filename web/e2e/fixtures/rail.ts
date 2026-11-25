import type { ProjectSummary, ResearchSummary } from '../../src/types'
import type { InboxResponse, ProjectSetSummary } from '../../src/generated/views'
import { ago, cost } from './clock'
import { longBranch, longURL } from './long'

// GET /api/v1/projects: the rail's projects.
export const projects: ProjectSummary[] = [
  {
    ...cost('48.20', 9_120_000),
    namespace: 'team-a',
    name: 'shop',
    gitlab: {
      url: 'https://gitlab.example.com',
      project: 'team-a/shop',
      webURL: 'https://gitlab.example.com/team-a/shop',
    },
    suspend: false,
    maxConcurrentRuns: 2,
    pollInterval: '1m',
    lastSyncTime: ago(1),
    tasks: 7,
    stages: { working: 1, review: 2, ci: 1, 'needs-input': 1, pending: 2 },
    createdAt: ago(60 * 24 * 40),
    autopilots: [
      { milestoneIID: 12, title: 'Checkout without account', phase: 'Running', done: 4, total: 9 },
      {
        milestoneIID: 14,
        title: 'Payment providers',
        phase: 'Paused',
        done: 2,
        total: 6,
        pausedReason: 'Issue #281 needs an answer to its question.',
      },
    ],
  },
  {
    ...cost('3.75', 610_000),
    namespace: 'team-b',
    name: 'a-project-with-a-rather-long-name.example',
    gitlab: {
      url: 'https://gitlab.example.com',
      project: 'team-b/a-project-with-a-rather-long-name.example',
      webURL: 'https://gitlab.example.com/team-b/a-project-with-a-rather-long-name.example',
    },
    suspend: true,
    maxConcurrentRuns: 1,
    pollInterval: '5m',
    tasks: 1,
    stages: { planned: 1 },
    createdAt: ago(60 * 24 * 3),
  },
]

// GET /api/v1/projectsets: none, so the rail is the flat list. The check
// "project sets in the rail" answers withSet and withSetProjects instead.
export const projectSets: ProjectSetSummary[] = []

const child = (name: string, path: string, stages: ProjectSummary['stages']): ProjectSummary => ({
  ...cost('0.90', 120_000),
  namespace: 'team-a',
  name,
  gitlab: { url: 'https://gitlab.example.com', project: path, webURL: `https://gitlab.example.com/${path}` },
  suspend: false,
  maxConcurrentRuns: 1,
  pollInterval: '2m',
  lastSyncTime: ago(3),
  tasks: 1,
  stages,
  createdAt: ago(60 * 24 * 2),
  set: 'team-a-group',
})

// One set in team-a with three children, for the check of the set group.
export const withSetProjects: ProjectSummary[] = [
  ...projects,
  child('tools-pnpm', 'team-a/tools/pnpm', { working: 1 }),
  child('docs', 'team-a/docs', { 'needs-input': 1 }),
  child('infra-cluster', 'team-a/infra/cluster', {}),
]

export const withSet: ProjectSetSummary[] = [
  {
    namespace: 'team-a',
    name: 'team-a-group',
    group: 'team-a',
    include: ['team-a/**'],
    exclude: ['team-a/archived/**'],
    pollInterval: '2m',
    lastDiscoveryTime: ago(1),
    counts: { active: 3, dormant: 12, excluded: 2, explicit: 1, nameTaken: 0 },
    projects: [],
    conditions: [
      { type: 'Ready', status: 'True', lastTransitionTime: ago(60), reason: 'Discovered', message: '' },
      { type: 'Discovered', status: 'True', lastTransitionTime: ago(60), reason: 'Discovered', message: '' },
    ],
  },
]

// GET /api/v1/inbox: one item of each common kind.
export const inbox: InboxResponse = {
  items: [
    {
      namespace: 'team-a',
      project: 'shop',
      kind: 'prompt',
      task: 'shop-281-implement',
      issueIID: 281,
      milestoneIID: 14,
      promptID: 'p-281-1',
      title: 'Support refunds through the payment provider',
      reason: 'question',
      at: ago(25),
      url: 'https://gitlab.example.com/team-a/shop/-/issues/281',
      key: 'team-a/shop/prompt/shop-281-implement/p-281-1',
      notify: 'question',
    },
    {
      namespace: 'team-a',
      project: 'shop',
      kind: 'pause',
      milestoneIID: 14,
      title: 'Payment providers',
      // Long data (long.ts): a branch and a URL that wrap in the context panel's card.
      reason: `Issue #281 needs an answer to its question **before** autopilot can continue with the next issue. Its branch is \`${longBranch}\`, the failed job ${longURL}.`,
      at: ago(24),
      pauseKind: 'needsInput',
      pausedSince: ago(24),
      key: 'team-a/shop/pause/14//needsInput',
      notify: 'problem',
    },
    {
      namespace: 'team-a',
      project: 'shop',
      kind: 'task',
      task: 'shop-277-implement',
      issueIID: 277,
      milestoneIID: 12,
      title: 'Guest checkout: keep the cart after the address step',
      // Long enough to be clipped at two lines on a phone (Truncated).
      reason:
        'The merge request has 2 blocking review findings: **the address step drops the cart** when the session expires, and the guest e-mail is not checked before the payment step starts.',
      at: ago(90),
      url: 'https://gitlab.example.com/team-a/shop/-/merge_requests/301',
      // Its reason is no stage, so it has no notify category.
      key: 'team-a/shop/task/shop-277-implement',
    },
    {
      namespace: 'team-a',
      project: 'shop',
      kind: 'proposal',
      proposalID: 9,
      milestoneIID: 12,
      title: 'Split the address form into its own component',
      reason: 'proposal',
      at: ago(60 * 5),
      key: 'team-a/shop/proposal/12/9',
      notify: 'proposal',
    },
  ],
  // The Handled view: newest first, one of each label.
  handled: [
    {
      namespace: 'team-a',
      project: 'shop',
      milestoneIID: 14,
      issueIID: 281,
      kind: 'prompt',
      promptID: 'Q3',
      title: 'Release SDK 4 before the refunds issue?',
      closedReason: 'answered',
      closedBy: 'owner',
      by: 'jo',
      at: ago(30),
    },
    {
      namespace: 'team-a',
      project: 'shop',
      milestoneIID: 14,
      issueIID: 282,
      kind: 'acted',
      title: 'Retry #282 after the flaky e2e job',
      at: ago(60 * 3),
    },
    {
      namespace: 'team-a',
      project: 'shop',
      milestoneIID: 12,
      kind: 'prompt',
      promptID: 'Q2',
      title: 'Main is red since `d4e5f6a`. Revert it?',
      closedReason: 'mainHealthy',
      closedBy: 'operator',
      at: ago(60 * 26),
    },
  ],
}

// GET /api/v1/researches: the rail's research section.
export const researches: ResearchSummary[] = [
  {
    namespace: 'research',
    name: 'payments-news',
    view: 'newsflash',
    depth: 'quick',
    schedule: '0 7 * * *',
    timeZone: 'Europe/Berlin',
    phase: 'Scheduled',
    lastRun: ago(60 * 5),
    nextRun: ago(-60 * 19),
    lastEngine: 'claude',
    lastModel: 'claude-sonnet-5-5',
    oneOff: false,
    canDelete: false,
  },
  {
    namespace: 'research',
    name: 'checkout-ux-study',
    view: 'report',
    depth: 'deep',
    phase: 'Succeeded',
    lastRun: ago(60 * 26),
    oneOff: true,
    createdBy: 'jo',
    canDelete: true,
  },
]
