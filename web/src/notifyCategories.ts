import type { NotifyCategory } from './notify.ts'

// The Notifications dialog's categories, in its order, with their one-line
// descriptions. Apart from notify.ts, which keeps only the defaults: the
// dialog's chunk loads this, the initial bundle does not.

export interface NotifyCategoryInfo {
  category: NotifyCategory
  label: string
  description: string
  // From history events: needs the history database.
  history?: boolean
}

export const notifyCategories: readonly NotifyCategoryInfo[] = [
  { category: 'problem', label: 'Problems', description: 'A stuck issue, a paused milestone or a hold.' },
  { category: 'question', label: 'Questions', description: 'A question from an agent, or a task that needs your input.' },
  { category: 'proposal', label: 'Proposals', description: 'A proposal that waits for your answer.' },
  { category: 'companion', label: 'Companions ready', description: 'A companion merge request ready to merge.' },
  { category: 'budget', label: 'Budget warnings', description: 'An issue or milestone near its budget.' },
  { category: 'plan', label: 'Plans ready', description: 'A plan agent answered your message.' },
  { category: 'milestoneStarted', label: 'Milestone started', description: "A milestone's issues are planned.", history: true },
  { category: 'milestoneDone', label: 'Milestone done', description: "A milestone's autopilot is done.", history: true },
  { category: 'release', label: 'Releases', description: 'A release is published or fails.', history: true },
  { category: 'failed', label: 'Failed or blocked tasks', description: 'A task failed or is blocked.' },
]
