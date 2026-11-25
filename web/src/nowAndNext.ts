import { liveLines, stepWords, type LiveLine } from './cockpitLive.ts'
import { duration } from './format.ts'
import type { Milestone, MilestoneView, QueueEntry, QueueItem, Task, WorkingNow } from './generated/views.ts'
import { queueDone, waitingLabel } from './milestoneQueue.ts'

// Now & next (R7 of "Desk: the Cockpit newest first by issue"): what a
// milestone runs now, what waits, what comes next and the milestones after
// it, computed from its queue, its tasks and the project queue.

// NowRow is one line of a section, "#712 implementing · 4 min". task, pod
// and run are set while the issue has a live run (LiveTranscript).
export interface NowRow {
  iid: number
  title: string
  text: string
  task?: string
  pod?: string
  run?: string
}

// NowGroup is the rows of a section under a heading: a milestone's on the
// project page, a project's on the Overview (D820-P3). A milestone page has
// one group without a heading.
export interface NowGroup {
  key: string
  heading?: string
  rows: NowRow[]
}

export interface NowMilestone {
  iid: number
  title: string
  text: string
  current: boolean
}

export interface NowCounts {
  running: number
  waiting: number
  next: number
  nextIID?: number
}

export interface NowAndNext {
  running: NowRow[]
  waiting: NowRow[]
  next: NowRow[]
  // moreNext: the items of Up next beyond nextMax ("+N more").
  moreNext: number
  milestones: NowMilestone[]
  counts: NowCounts
}

export interface NowAndNextInput {
  milestone: Pick<MilestoneView, 'iid' | 'title'>
  items: readonly QueueItem[] | undefined
  tasks: readonly Task[] | undefined
  queue: readonly QueueEntry[] | undefined
  now: number
}

// nextMax: Up next shows this many items; nextMilestones: the milestones
// after this one.
const nextMax = 5
const nextMilestones = 3

// ciWaits: the CI states in which a pipeline is still to finish.
const ciWaits = new Set(['Pending', 'Running'])

// nowAndNext sorts the milestone's queue items into the sections, each in
// queue order (laneRows).
export function nowAndNext({ milestone, items, tasks, queue, now }: NowAndNextInput): NowAndNext {
  const { running, waiting, upNext } = laneRows(items, tasks, now)
  return {
    running,
    waiting,
    ...nextRows(upNext),
    milestones: milestoneRows(milestone, items ?? [], queue ?? []),
    counts: { running: running.length, waiting: waiting.length, next: upNext.length, nextIID: upNext[0]?.iid },
  }
}

function nextRows(upNext: NowRow[]): Pick<NowAndNext, 'next' | 'moreNext'> {
  return { next: upNext.slice(0, nextMax), moreNext: Math.max(0, upNext.length - nextMax) }
}

// laneRows sorts queue items into running, waiting and up next (ready, then
// waiting), each in queue order. An item is in at most one of running and
// waiting. A running item with a run in progress works: its step from
// liveLines, "reviewing" too, as the reviewers run inside the run. Without a
// run in progress, while the pipeline of its pushed head runs, it waits for
// CI. The runner has no step that blocks on CI or a review, and on autopilot
// no human reviews.
function laneRows(items: readonly QueueItem[] | undefined, tasks: readonly Task[] | undefined, now: number) {
  const live = new Map(liveLines(tasks, now).map((l) => [l.iid, l]))
  const taskByName = new Map((tasks ?? []).map((t) => [t.name, t]))
  const running: NowRow[] = []
  const waiting: NowRow[] = []
  const ready: NowRow[] = []
  const queued: NowRow[] = []
  for (const it of items ?? []) {
    const line = live.get(it.iid)
    const row = (text: string): NowRow => ({ iid: it.iid, title: it.title, text, ...liveRun(line) })
    switch (it.lane) {
      case 'landing':
        running.push(row(`#${it.iid} merging`))
        break
      case 'running': {
        const task = it.task ? taskByName.get(it.task) : undefined
        if (line) running.push(row(line.text))
        else if (ciWaits.has(task?.status.ci?.state ?? '')) waiting.push(row(`#${it.iid} waiting for CI`))
        else running.push(row(`#${it.iid} ${task?.status.phase === 'Queued' ? 'queued' : 'starting'}`))
        break
      }
      case 'held': {
        const reason = it.hold?.reason?.trim()
        waiting.push(row(reason ? `#${it.iid} held: ${reason}` : `#${it.iid} held`))
        break
      }
      case 'ready':
        ready.push(row(`#${it.iid} ready to land`))
        break
      case 'waiting':
        queued.push(row(it.waitingFor?.length ? `#${it.iid} ${waitingLabel(it.waitingFor)}` : `#${it.iid} waiting ${waitingLabel()}`))
        break
    }
  }
  return { running, waiting, upNext: [...ready, ...queued] }
}

// ProjectNowAndNext is Now & next across a project (R9): Running and
// Waiting grouped by milestone (a group's key is its milestone's IID), Up
// next of the milestone that runs first (nextOf), and the project queue.
export interface ProjectNowAndNext extends Omit<NowAndNext, 'running' | 'waiting'> {
  running: NowGroup[]
  waiting: NowGroup[]
  nextOf?: number
}

export interface ProjectNowAndNextInput {
  // milestones: the project's, with their implement tasks and spec task.
  milestones: readonly Pick<Milestone, 'iid' | 'title' | 'tasks' | 'spec'>[] | undefined
  // first: the loaded view of the milestone that runs first; its queue
  // items are Up next.
  first: Pick<MilestoneView, 'iid' | 'autopilot' | 'tasks'> | null | undefined
  queue: readonly QueueEntry[] | undefined
  now: number
}

// projectNowAndNext: the project page has each milestone's tasks and spec
// task, not its queue lanes (D822-P1). So a milestone's issues that run now
// (a task in phase Running or Queued) or wait for CI are lane running, and
// its Active spec items with a hold lane held, sorted by laneRows' rules. A
// milestone without such issues has no group.
export function projectNowAndNext({ milestones, first, queue, now }: ProjectNowAndNextInput): ProjectNowAndNext {
  const running: NowGroup[] = []
  const waiting: NowGroup[] = []
  for (const m of milestones ?? []) {
    const rows = laneRows(projectItems(m), m.tasks, now)
    const key = String(m.iid)
    if (rows.running.length) running.push({ key, heading: m.title, rows: rows.running })
    if (rows.waiting.length) waiting.push({ key, heading: m.title, rows: rows.waiting })
  }
  const upNext = first ? laneRows(first.autopilot?.items, first.tasks, now).upNext : []
  const count = (groups: NowGroup[]) => groups.reduce((n, g) => n + g.rows.length, 0)
  return {
    running,
    waiting,
    ...nextRows(upNext),
    nextOf: first?.iid,
    milestones: (queue ?? []).map((e) => ({ iid: e.iid, title: e.title, text: `${e.state} · ${mergedText(e.done, e.total)}`, current: false })),
    counts: { running: count(running), waiting: count(waiting), next: upNext.length, nextIID: upNext[0]?.iid },
  }
}

// projectItems: a project milestone's issues as the queue items laneRows
// sorts, by issue: held, running or waiting for CI. An issue whose spec
// item is merged, skipped or pending has none.
function projectItems(m: Pick<Milestone, 'tasks' | 'spec'>): QueueItem[] {
  const spec = new Map((m.spec?.status.autopilot?.items ?? []).map((i) => [i.iid, i]))
  const items: QueueItem[] = []
  for (const t of m.tasks) {
    const iid = t.issue?.iid
    const it = iid ? spec.get(iid) : undefined
    if (!iid || (it && it.state !== 'Active')) continue
    const base = { iid, title: t.issue?.title ?? '', state: 'Active' as const, task: t.name }
    if (it?.hold) items.push({ ...base, lane: 'held', hold: it.hold })
    else if (t.status.phase === 'Running' || t.status.phase === 'Queued' || ciWaits.has(t.status.ci?.state ?? '')) {
      items.push({ ...base, lane: 'running' })
    }
  }
  // A hold without a task (none in practice: a hold is over a run).
  const shown = new Set(items.map((i) => i.iid))
  for (const it of spec.values()) {
    if (it.hold && it.state === 'Active' && !shown.has(it.iid)) items.push({ iid: it.iid, title: '', state: 'Active', lane: 'held', hold: it.hold })
  }
  return items.sort((a, b) => a.iid - b.iid)
}

function liveRun(line: LiveLine | undefined): Pick<NowRow, 'task' | 'pod' | 'run'> {
  if (!line) return {}
  return line.pod ? { task: line.task, pod: line.pod, run: line.run } : { task: line.task, run: line.run }
}

// milestoneRows: this milestone, "3 of 9 merged", then the next ones of the
// project queue (the first ones when this milestone is not in it).
function milestoneRows(m: Pick<MilestoneView, 'iid' | 'title'>, items: readonly QueueItem[], queue: readonly QueueEntry[]): NowMilestone[] {
  const at = queue.findIndex((e) => e.iid === m.iid)
  const after = queue.filter((e) => e.iid !== m.iid).slice(Math.max(0, at), Math.max(0, at) + nextMilestones)
  return [
    { iid: m.iid, title: m.title, text: mergedText(queueDone(items), items.length), current: true },
    ...after.map((e) => ({ iid: e.iid, title: e.title, text: mergedText(e.done, e.total), current: false })),
  ]
}

function mergedText(done: number, total: number): string {
  return total > 0 ? `${done} of ${total} merged` : 'not planned yet'
}

// OverviewProjectNow is Now & next of one project on the Overview (R10):
// Running and Waiting grouped by milestone (a group's key is its
// milestone's IID, "0" without one; the groups have no heading), Up next the
// project's next item, and its running and next milestone.
export interface OverviewProjectNow {
  key: string
  namespace: string
  project: string
  finishing: number
  running: NowGroup[]
  waiting: NowGroup[]
  next: NowRow[]
  milestones: NowMilestone[]
}

export interface OverviewNowAndNext {
  projects: OverviewProjectNow[]
  counts: NowCounts
}

// overviewNowAndNext sorts the Overview's Working now entries by project.
// An entry with a running task works: its step in words and the minutes of
// the step, "starting" before its first step; without a step while its
// pushed head's pipeline runs it waits for CI (laneRows' rules). Waiting
// adds the project's issues between runs that wait for CI and its held
// issues. A held issue is only in Waiting, as lane held is on a milestone
// (laneRows): its running task or CI wait adds no row. A row's task is set while it
// runs (the link without a milestone). The counts are across all projects;
// next is the first project's.
export function overviewNowAndNext(working: readonly WorkingNow[] | undefined, now: number): OverviewNowAndNext {
  const projects = new Map<string, OverviewProjectNow>()
  const groups = new Map<string, { running: Map<number, NowRow[]>; waiting: Map<number, NowRow[]>; held: Set<string> }>()
  const add = (to: Map<number, NowRow[]>, milestone: number | undefined, row: NowRow) =>
    to.set(milestone ?? 0, [...(to.get(milestone ?? 0) ?? []), row])
  for (const w of working ?? []) {
    const key = `${w.namespace}/${w.project}`
    let g = groups.get(key)
    if (!g) {
      g = { running: new Map(), waiting: new Map(), held: new Set((w.held ?? []).map((h) => `${h.milestoneIID}/${h.issueIID}`)) }
      groups.set(key, g)
      for (const c of w.ciWaits ?? []) {
        if (!g.held.has(`${c.milestoneIID ?? 0}/${c.issueIID}`)) add(g.waiting, c.milestoneIID, { iid: c.issueIID, title: c.title, text: `#${c.issueIID} waiting for CI` })
      }
      for (const h of w.held ?? []) {
        const reason = h.reason?.trim()
        add(g.waiting, h.milestoneIID, { iid: h.issueIID, title: h.title ?? '', text: reason ? `#${h.issueIID} held: ${reason}` : `#${h.issueIID} held` })
      }
      projects.set(key, {
        key,
        namespace: w.namespace,
        project: w.project,
        finishing: w.finishing ?? 0,
        running: [],
        waiting: [],
        next: w.next ? [{ iid: w.next.issueIID, title: w.next.title, text: `#${w.next.issueIID}` }] : [],
        milestones: (w.milestones ?? []).map((m) => ({ iid: m.iid, title: m.title, text: m.state, current: false })),
      })
    }
    if (!w.issueIID || !w.task || g.held.has(`${w.milestoneIID ?? 0}/${w.issueIID}`)) continue
    const row = (text: string): NowRow => ({ iid: w.issueIID!, title: w.title ?? '', text, task: w.task })
    if (w.step) {
      const since = Date.parse(w.stepSince ?? '')
      const parts = [`#${w.issueIID} ${stepWords(w.step)}`]
      if (!Number.isNaN(since)) parts.push(duration(Math.max(0, now - since)))
      add(g.running, w.milestoneIID, row(parts.join(' · ')))
    } else if (ciWaits.has(w.ci ?? '')) add(g.waiting, w.milestoneIID, row(`#${w.issueIID} waiting for CI`))
    else add(g.running, w.milestoneIID, row(`#${w.issueIID} starting`))
  }
  const sorted = (m: Map<number, NowRow[]>): NowGroup[] =>
    [...m.entries()].sort(([a], [b]) => a - b).map(([iid, rows]) => ({ key: String(iid), rows: rows.sort((a, b) => a.iid - b.iid) }))
  const count = (groups: NowGroup[]) => groups.reduce((n, g) => n + g.rows.length, 0)
  const out = [...projects.values()].map((p) => ({ ...p, running: sorted(groups.get(p.key)!.running), waiting: sorted(groups.get(p.key)!.waiting) }))
  const next = out.flatMap((p) => p.next)
  return {
    projects: out,
    counts: {
      running: out.reduce((n, p) => n + count(p.running), 0),
      waiting: out.reduce((n, p) => n + count(p.waiting), 0),
      next: next.length,
      nextIID: next[0]?.iid,
    },
  }
}

// nowStripText is the one-line strip under the header on narrow screens,
// "2 running · 1 waiting · next #717", without the parts that are zero.
// Without anything running or next it starts with "Nothing running".
export function nowStripText(c: NowCounts): string {
  const parts: string[] = []
  if (c.running > 0) parts.push(`${c.running} running`)
  else if (!c.nextIID) parts.push('Nothing running')
  if (c.waiting > 0) parts.push(`${c.waiting} waiting`)
  if (c.nextIID) parts.push(`next #${c.nextIID}`)
  return parts.join(' · ')
}
