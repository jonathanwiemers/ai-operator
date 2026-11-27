import type { MilestoneProgress, MilestoneRow, MilestoneState, ProjectSummary, WorkingNow } from './generated/views.ts'
import type { InboxItem } from './types.ts'

// Project cards (R4 of "Desk: Milestones and Issues first, a Timeline tab,
// and what each project builds"): what each project builds now, what comes
// next and whether it needs you. Pure: no DOM, no fetches.

interface CardMilestone {
  iid: number
  title: string
  state: MilestoneState
  progress: MilestoneProgress
}

interface RunningIssue {
  iid: number
  title: string
  step?: string
  stepSince?: string
}

interface IssueGroup {
  milestoneIID?: number
  title: string
  issues: RunningIssue[]
}

export interface ProjectCard {
  namespace: string
  name: string
  needsYou: number
  building: CardMilestone[]
  runningIssues: IssueGroup[]
  upNext: CardMilestone[]
  moreQueued: number
  planned: number
  finishing: number
  suspended: boolean
}

const UP_NEXT = 3
const LOOSE = 'Loose issues'

const key = (namespace: string, name: string) => `${namespace}/${name}`

function byName(a: ProjectCard, b: ProjectCard): number {
  return a.name.localeCompare(b.name) || a.namespace.localeCompare(b.namespace)
}

function group<T extends { namespace: string; project: string }>(rows: T[]): Map<string, T[]> {
  const out = new Map<string, T[]>()
  for (const r of rows) {
    const k = key(r.namespace, r.project)
    const list = out.get(k)
    if (list) list.push(r)
    else out.set(k, [r])
  }
  return out
}

function groupIssues(entries: WorkingNow[], building: CardMilestone[], titles: Map<number, string>): IssueGroup[] {
  const byMilestone = new Map<number, RunningIssue[]>()
  const loose: RunningIssue[] = []
  for (const w of entries) {
    if (!w.issueIID) continue
    const issue: RunningIssue = { iid: w.issueIID, title: w.title ?? '' }
    if (w.step) issue.step = w.step
    if (w.stepSince) issue.stepSince = w.stepSince
    if (w.milestoneIID) {
      const list = byMilestone.get(w.milestoneIID)
      if (list) list.push(issue)
      else byMilestone.set(w.milestoneIID, [issue])
    } else loose.push(issue)
  }
  const buildingIIDs = building.map((m) => m.iid).filter((iid) => byMilestone.has(iid))
  const others = [...byMilestone.keys()].filter((iid) => !buildingIIDs.includes(iid)).sort((a, b) => a - b)
  const groups: IssueGroup[] = [...buildingIIDs, ...others].map((iid) => ({
    milestoneIID: iid,
    title: titles.get(iid) ?? `#${iid}`,
    issues: byMilestone.get(iid) ?? [],
  }))
  if (loose.length) groups.push({ title: LOOSE, issues: loose })
  return groups
}

export function projectCards(
  projects: ProjectSummary[],
  milestones: MilestoneRow[],
  working: WorkingNow[],
  inbox: InboxItem[],
): { cards: ProjectCard[]; idle: ProjectCard[] } {
  const ms = group(milestones)
  const ws = group(working)
  const ib = group(inbox)
  const first: ProjectCard[] = []
  const second: ProjectCard[] = []
  const third: ProjectCard[] = []
  const idle: ProjectCard[] = []
  for (const p of projects) {
    const k = key(p.namespace, p.name)
    const rows = ms.get(k) ?? []
    const card = (m: MilestoneRow): CardMilestone => ({ iid: m.iid, title: m.title, state: m.state, progress: m.progress })
    const building = rows.filter((m) => m.state === 'running' || m.state === 'paused').sort((a, b) => a.iid - b.iid).map(card)
    const queued = rows
      .filter((m) => m.state === 'waiting' && (m.queuePosition ?? 0) > 0)
      .sort((a, b) => (a.queuePosition ?? 0) - (b.queuePosition ?? 0))
    const titles = new Map(rows.map((m) => [m.iid, m.title]))
    const c: ProjectCard = {
      namespace: p.namespace,
      name: p.name,
      needsYou: (ib.get(k) ?? []).filter((i) => i.kind !== 'plan').length,
      building,
      runningIssues: groupIssues(ws.get(k) ?? [], building, titles),
      upNext: queued.slice(0, UP_NEXT).map(card),
      moreQueued: Math.max(0, queued.length - UP_NEXT),
      planned: rows.filter((m) => m.state === 'planned').length,
      finishing: p.settlingMilestones ?? 0,
      suspended: p.suspend,
    }
    if (c.needsYou > 0) first.push(c)
    else if (c.building.length > 0 || c.runningIssues.length > 0) second.push(c)
    else if (c.upNext.length > 0 || c.moreQueued > 0) third.push(c)
    else idle.push(c)
  }
  first.sort(byName)
  second.sort(byName)
  third.sort(byName)
  idle.sort(byName)
  return { cards: [...first, ...second, ...third], idle }
}
