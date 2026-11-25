import { duration } from './format.ts'
import type { Task } from './generated/views.ts'
import { runInProgress } from './taskHealth.ts'

// liveLines: one line per issue whose implementer runs now, "#712
// implementing · 4 min", for the Now & next sidebar and the thread headers.

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
