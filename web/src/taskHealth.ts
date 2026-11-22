import { durationShort, phaseTone, plural, type Tone } from './format.ts'
import type { RunStatus, TaskPhase } from './generated/api.ts'
import type { TaskHealth } from './generated/views.ts'

// taskStatus: the task's one status, the eyebrow of its header (decision 4,
// D627-P1): Suspended while suspended, otherwise its phase ("Running").
// The stage ("Working") and the pod phase are not shown beside it.
export function taskStatus(t: { suspend?: boolean; status?: { phase?: TaskPhase } }): { label: string; tone: Tone } {
  if (t.suspend) return { label: 'Suspended', tone: 'warning' }
  const phase = t.status?.phase
  return { label: phase || 'Pending', tone: phaseTone(phase) }
}

// taskStep: the step the run is in, from the run status, else from the
// health; '' when neither has one. since is the health's stepSince, only
// when the step shown is the health's step: the run's step with the age of
// another step would mislead.
export function taskStep(run: RunStatus | undefined, h: TaskHealth | undefined | null): { text: string; since?: string } {
  const text = run?.currentStep || h?.step || ''
  return text && text === h?.step ? { text, since: h.stepSince } : { text }
}

// restartsText: "N restarts" for a pod, with the last termination reason
// in parentheses when the API set one; '' without restarts (D627-P1: a
// non-zero count is a warning, "0 restarts" is noise). Restarts omitempty
// to 0.
export function restartsText(h: TaskHealth | undefined | null): string {
  const n = h?.restarts ?? 0
  if (n <= 0) return ''
  const base = `${n} ${plural(n, 'restart')}`
  return h?.lastTermination ? `${base} (${h.lastTermination})` : base
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
