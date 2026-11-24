import type { ChatPage, PlanListItem, PlanOverview, PlanView } from '../../src/generated/views'
import { ago } from './clock'

// GET /api/v1/plans?project=team-a/shop: one plan per kind of row, with a
// long title to check that rows wrap on a phone.
function plan(p: Partial<PlanListItem> & Pick<PlanListItem, 'name' | 'title' | 'phase' | 'lastActivityAt'>): PlanListItem {
  return {
    namespace: 'team-a',
    project: 'shop',
    createdBy: 'jo',
    createdAt: ago(60 * 24 * 3),
    spendUSD: '1.2',
    tokens: 120_000,
    budgetUSD: '5',
    budgetLimitUSD: '20',
    draftsTotal: 1,
    draftsCreated: 0,
    createdMilestones: [],
    permissions: { steer: true },
    ...p,
  }
}

const shopPlans: PlanListItem[] = [
  plan({ name: 'plan-guest', title: 'Guest checkout: confirmation mail and a receipt for every split shipment', phase: 'Working', lastActivityAt: ago(5) }),
  plan({ name: 'plan-broken', title: '', phase: 'Failed', lastActivityAt: ago(90) }),
  plan({
    name: 'plan-refunds',
    title: 'Refunds through the payment provider',
    phase: 'Created',
    lastActivityAt: ago(60 * 24),
    createdMilestoneIID: 14,
    createdMilestoneURL: 'https://gitlab.example.com/team-a/shop/-/milestones/14',
    draftsCreated: 1,
    createdMilestones: [{ iid: 14, url: 'https://gitlab.example.com/team-a/shop/-/milestones/14' }],
    milestone: { state: 'running', progress: { merged: 2, total: 4, open: 2 }, stored: false },
  }),
  plan({
    name: 'plan-search',
    title: 'Search: typo tolerance',
    phase: 'Created',
    lastActivityAt: ago(60 * 24 * 2),
    createdMilestoneIID: 15,
    createdMilestoneURL: 'https://gitlab.example.com/team-a/shop/-/milestones/15',
    draftsCreated: 1,
    createdMilestones: [{ iid: 15, url: 'https://gitlab.example.com/team-a/shop/-/milestones/15' }],
  }),
]

const shopPlanOverview: PlanOverview = {
  plans: shopPlans,
  projects: [{ namespace: 'team-a', name: 'shop', path: 'team-a/shop', steer: true }],
}

// GET /api/v1/plans without ?project: shop, a set child, a dormant entry of
// the set, its dormant entry under the name of shop (shown once, as shop)
// and a project the caller may only view (not offered on New plan).
const allPlanOverview: PlanOverview = {
  plans: shopPlans,
  projects: [
    ...shopPlanOverview.projects,
    { namespace: 'team-a', name: 'tools-pnpm', path: 'team-a/tools/pnpm', set: 'team-a-group', steer: true },
    { namespace: 'team-a', name: 'tools-foo', path: 'team-a/tools/foo', set: 'team-a-group', dormant: true, steer: true },
    { namespace: 'team-a', name: 'shop', path: 'team-a/shop', set: 'team-a-group', dormant: true, steer: true },
    { namespace: 'team-b', name: 'web', path: 'team-b/web', steer: false },
  ],
}

export const answerPlans = (q: URLSearchParams): PlanOverview => (q.get('project') ? shopPlanOverview : allPlanOverview)

// GET …/tools-foo/plans/plan-wait: a plan in the dormant set entry, waiting
// for the set to create its project (phase Starting with the message).
const waitPath = '/projects/team-a/tools-foo/plans/plan-wait'
const waitingPlan: PlanView = {
  ...plan({
    name: 'plan-wait',
    project: 'tools-foo',
    title: 'CSV export on the Metrics page',
    phase: 'Starting',
    message: 'Waiting for the project set to create the project',
    createdAt: ago(1),
    lastActivityAt: ago(1),
    spendUSD: '0',
    tokens: 0,
  }),
  drafts: [],
  decisions: [],
  decisionsPending: 0,
}
const waitingChat: ChatPage = { messages: [] }
export const planAPI: Record<string, unknown> = { [waitPath]: waitingPlan, [`${waitPath}/chat`]: waitingChat }
