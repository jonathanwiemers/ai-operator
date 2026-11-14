// Pure helpers for the plans list (title fallback, phase badge tone).
import type { AgentPlanPhase } from './generated/api'
import type { Tone } from './format'

// planTitle is the plan's title, or "Untitled plan · <date>" when empty
// (D448-P1).
export function planTitle(title: string, createdAt: string): string {
  const t = title.trim()
  if (t) return t
  const d = new Date(createdAt)
  const when = Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString()
  return when ? `Untitled plan · ${when}` : 'Untitled plan'
}

// planPhaseTone maps an AgentPlan phase to a Desk badge tone.
export function planPhaseTone(phase: AgentPlanPhase | undefined): Tone {
  switch (phase) {
    case 'Working':
      return 'progress'
    case 'Starting':
    case 'Ready':
      return 'info'
    case 'Created':
      return 'success'
    case 'Failed':
      return 'danger'
    case 'Idle':
      return 'muted'
    default:
      return 'muted'
  }
}
