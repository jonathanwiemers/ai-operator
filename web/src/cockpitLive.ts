import { duration } from './format.ts'
import type { Task, WorkingNow } from './generated/views.ts'
import { runInProgress } from './taskHealth.ts'

// The Cockpit's live lines (R11 of "Desk: the Cockpit"): one line per issue
// whose implementer runs now, "#712 implementing · 4 min".

export interface LiveLine {
  task: string
  iid: number
  // text: "#712 implementing · 4 min", without the minutes while the run
  // has no step yet.
  text: string
  // pod and run open the task's transcript (LiveTranscript).
  pod?: string
  run: string
}

// stepWords: the runner's step label ("3 run implementer, step 2", "5 fix
// round 1") in words. A step without words of its own shows its name.
const stepWordList: [RegExp, string][] = [
  [/^run implementer/, 'implementing'],
  [/^fix round/, 'fixing review findings'],
  [/^(review round|request review)/, 'reviewing'],
  [/^(read failed CI jobs|start pipeline)/, 'fixing CI'],
  [/^(clone|fetch) repository|^prepare /, 'preparing'],
  [/^(commit|push)/, 'pushing'],
  [/^(open|find|mark|link) .*merge request|^mark (ready|draft)/, 'updating the merge request'],
  [/^(merge|conclude merge)/, 'merging'],
  [/^save checkpoint/, 'saving a checkpoint'],
  [/^read /, 'reading'],
]

export function stepWords(label: string | undefined): string {
  const name = (label ?? '').replace(/^\d+\s+/, '').trim()
  if (!name) return 'starting'
  for (const [re, words] of stepWordList) if (re.test(name)) return words
  return name
}

// liveLines: the milestone's implement tasks with a run in progress (phase
// Running, and a run request open or a step), by issue. A finished run (phase
// Idle, even with its last step left) has no line.
export function liveLines(tasks: readonly Task[] | undefined, now: number): LiveLine[] {
  const lines: LiveLine[] = []
  for (const t of tasks ?? []) {
    const run = t.status.run
    if (t.type !== 'Implement' || !t.issue || t.status.phase !== 'Running' || !runInProgress(run)) continue
    const parts = [`#${t.issue.iid} ${stepWords(run?.currentStep)}`]
    const since = run?.currentStep ? Date.parse(run.currentStepSince ?? '') : NaN
    if (!Number.isNaN(since)) parts.push(duration(Math.max(0, now - since)))
    lines.push({
      task: t.name,
      iid: t.issue.iid,
      text: parts.join(' · '),
      pod: t.status.podName,
      run: String(run?.activeRunRequest ?? ''),
    })
  }
  return lines.sort((a, b) => a.iid - b.iid)
}

// OverviewLine is a live line of the Overview Cockpit (R16): one per
// running issue of Working now, across projects, "shop #712 Add the
// provider · 4 min". It links to the issue's milestone Cockpit, else to its
// task.
export interface OverviewLine {
  key: string
  text: string
  to:
    | { name: 'stream-milestone'; params: { namespace: string; name: string; iid: string } }
    | { name: 'task'; params: { namespace: string; name: string; task: string } }
}

// overviewLines: the Working now entries that hold a run (an issue and a
// task), by project and issue. A project without a running issue has none.
export function overviewLines(working: readonly WorkingNow[] | undefined, now: number): OverviewLine[] {
  const lines: (OverviewLine & { sort: string })[] = []
  for (const w of working ?? []) {
    if (!w.issueIID || !w.task) continue
    const base = { namespace: w.namespace, name: w.project }
    const parts = [`${w.project} #${w.issueIID}${w.title ? ` ${w.title}` : ''}`]
    const since = Date.parse(w.since ?? '')
    if (!Number.isNaN(since)) parts.push(duration(Math.max(0, now - since)))
    lines.push({
      key: `${w.namespace}/${w.project}/${w.task}`,
      text: parts.join(' · '),
      to: w.milestoneIID
        ? { name: 'stream-milestone', params: { ...base, iid: String(w.milestoneIID) } }
        : { name: 'task', params: { ...base, task: w.task } },
      sort: `${w.namespace}/${w.project}/${String(w.issueIID).padStart(10, '0')}`,
    })
  }
  return lines.sort((a, b) => a.sort.localeCompare(b.sort)).map(({ sort: _, ...l }) => l)
}
