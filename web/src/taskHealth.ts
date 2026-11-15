import { durationShort } from './format.ts'
import type { RunStatus } from './generated/api.ts'
import type { TaskHealth } from './generated/views.ts'

// showHealthLine: the compact run-health row above the task tabs. Hidden
// when there is no health, or neither a pod phase nor a step.
export function showHealthLine(h: TaskHealth | undefined | null): boolean {
  return !!(h && (h.podPhase || h.step))
}

// restartsText: "N restarts" for a pod, with the last termination reason
// in parentheses when the API set one. Restarts omitempty to 0.
export function restartsText(h: TaskHealth): string {
  const n = h.restarts ?? 0
  const base = `${n} restart${n === 1 ? '' : 's'}`
  return h.lastTermination ? `${base} (${h.lastTermination})` : base
}

// stepAgeText: how long the current step has been running, from stepSince
// to now, in the short duration form ("42m"). Empty when since is missing
// or invalid.
export function stepAgeText(stepSince: string | undefined, now: number): string {
  if (!stepSince) return ''
  const t = Date.parse(stepSince)
  if (Number.isNaN(t)) return ''
  return durationShort(Math.max(0, (now - t) / 1000))
}

// runInProgress: the runner took a run request it has not finished, or is
// in a step. The Restart run button is enabled only then.
export function runInProgress(run: RunStatus | undefined): boolean {
  return (run?.activeRunRequest ?? 0) > (run?.observedRunRequest ?? 0) || !!run?.currentStep
}
