import { liveLines, type LiveLine } from './cockpitLive.ts'
import type { MilestoneView, QueueEntry, QueueItem, Task } from './generated/views.ts'
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
// queue order. An item is in at most one of running and waiting. A running
// item with a run in progress works: its step from liveLines, "reviewing"
// too, as the reviewers run inside the run. Without a run in progress, while
// the pipeline of its pushed head runs, it waits for CI. The runner has no
// step that blocks on CI or a review, and on autopilot no human reviews.
export function nowAndNext({ milestone, items, tasks, queue, now }: NowAndNextInput): NowAndNext {
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
  const upNext = [...ready, ...queued]
  return {
    running,
    waiting,
    next: upNext.slice(0, nextMax),
    moreNext: Math.max(0, upNext.length - nextMax),
    milestones: milestoneRows(milestone, items ?? [], queue ?? []),
    counts: { running: running.length, waiting: waiting.length, next: upNext.length, nextIID: upNext[0]?.iid },
  }
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
