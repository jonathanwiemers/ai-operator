import type { InboxResponse, ProjectSummary, ResearchSummary } from '../../src/types'
import { ago, cost } from './clock'

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
    },
    {
      namespace: 'team-a',
      project: 'shop',
      kind: 'pause',
      milestoneIID: 14,
      title: 'Payment providers',
      reason: 'Issue #281 needs an answer to its question **before** autopilot can continue with the next issue.',
      at: ago(24),
      pauseKind: 'needsInput',
      pausedSince: ago(24),
    },
    {
      namespace: 'team-a',
      project: 'shop',
      kind: 'task',
      task: 'shop-277-implement',
      issueIID: 277,
      milestoneIID: 12,
      title: 'Guest checkout: keep the cart after the address step',
      reason: 'The merge request has 2 blocking review findings.',
      at: ago(90),
      url: 'https://gitlab.example.com/team-a/shop/-/merge_requests/301',
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
