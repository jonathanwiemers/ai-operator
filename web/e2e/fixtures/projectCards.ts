import type { MilestoneList, MilestoneRow, OverviewView, ProjectSummary } from '../../src/types'
import { ago } from './clock'
import { openMilestones, overview } from './overview'
import { inbox, projects } from './rail'

// The Overview's Projects tab (overview-projects.spec.ts, R5 of "Desk:
// Milestones and Issues first"): team-a/shop needs you (the default Inbox
// is shop's), runs %12 with two issues running and has four milestones
// queued; team-b/api has one queued only; team-a/docs is idle and
// team-c/blog only finishing.

const base = projects[0]!
const project = (namespace: string, name: string, over: Partial<ProjectSummary> = {}): ProjectSummary => ({
  ...base,
  namespace,
  name,
  gitlab: { ...base.gitlab, project: `${namespace}/${name}`, webURL: `https://gitlab.example.com/${namespace}/${name}` },
  suspend: false,
  settlingMilestones: 0,
  autopilots: [],
  ...over,
})

export const cardProjects: ProjectSummary[] = [
  project('team-a', 'docs'),
  project('team-c', 'blog', { settlingMilestones: 1 }),
  project('team-b', 'api', { suspend: true }),
  project('team-a', 'shop', { settlingMilestones: 2 }),
]

const row = openMilestones.milestones[0]!
const milestone = (namespace: string, name: string, iid: number, title: string, over: Partial<MilestoneRow>): MilestoneRow => ({
  ...row,
  namespace,
  project: name,
  iid,
  title,
  webURL: `https://gitlab.example.com/${namespace}/${name}/-/milestones/${iid}`,
  needsYou: false,
  ...over,
})

const queued = (iid: number, title: string, place: number) =>
  milestone('team-a', 'shop', iid, title, { state: 'waiting', phase: 'waiting', queuePosition: place, progress: { merged: 0, total: 4, open: 4 } })

export const cardMilestones: MilestoneList = {
  milestones: [
    milestone('team-a', 'shop', 12, 'Checkout without account', { state: 'running', phase: 'running', queuePosition: 1, progress: { merged: 3, total: 7, open: 4 }, needsYou: true }),
    queued(18, 'Gift cards', 5),
    queued(15, 'Wish lists', 2),
    queued(17, 'Order history export', 4),
    queued(16, 'Coupons', 3),
    milestone('team-a', 'shop', 19, 'Loyalty points', { state: 'planned', progress: { merged: 0, total: 2, open: 2 } }),
    milestone('team-b', 'api', 3, 'Rate limits', { state: 'waiting', phase: 'waiting', queuePosition: 1, progress: { merged: 0, total: 3, open: 3 } }),
  ],
  history: true,
}

export const cardOverview: OverviewView = {
  ...overview,
  working: [
    {
      ...overview.working[0]!,
      issueIID: 277,
      title: 'Guest checkout: keep the cart after the address step',
      milestoneIID: 12,
      task: 'shop-277-implement',
      step: '3 run implementer',
      stepSince: ago(4),
    },
    {
      namespace: 'team-a',
      project: 'shop',
      issueIID: 283,
      title: 'Guest checkout: confirmation mail',
      milestoneIID: 12,
      task: 'shop-283-implement',
      since: ago(20),
      step: '2 review',
      stepSince: ago(9),
    },
  ],
}

// The Inbox: shop's items, so shop needs you.
export const cardInbox = inbox
export const shopNeeds = inbox.items.filter((i) => i.project === 'shop' && i.kind !== 'plan').length
