import type { PlanListItem, PlanOverview } from '../../src/generated/views'
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

export const shopPlanOverview: PlanOverview = {
  plans: shopPlans,
  projects: [{ namespace: 'team-a', name: 'shop', steer: true }],
}
