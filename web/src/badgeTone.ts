// How a Desk tone looks on Nuxt UI's UBadge (docs/design.md, "Badge tones").
// The class strings are literal so Tailwind finds them.
import type { Tone } from './format'

export interface BadgeLook {
  color: 'neutral' | 'info' | 'secondary' | 'success' | 'warning' | 'error'
  variant: 'soft'
  // Where the kit's soft variant would differ from the Desk's tone tokens.
  class: string
}

export const badgeLook: Record<Tone, BadgeLook> = {
  neutral: { color: 'neutral', variant: 'soft', class: 'bg-(--tone-neutral-bg) text-(--tone-neutral-fg)' },
  muted: { color: 'neutral', variant: 'soft', class: 'bg-(--tone-muted-bg) text-(--tone-muted-fg)' },
  info: { color: 'info', variant: 'soft', class: 'bg-(--tone-info-bg) text-(--tone-info-fg)' },
  progress: { color: 'secondary', variant: 'soft', class: 'bg-(--tone-progress-bg) text-(--tone-progress-fg)' },
  success: { color: 'success', variant: 'soft', class: 'bg-(--tone-success-bg) text-(--tone-success-fg)' },
  warning: { color: 'warning', variant: 'soft', class: 'bg-(--tone-warning-bg) text-(--tone-warning-fg)' },
  danger: { color: 'error', variant: 'soft', class: 'bg-(--tone-danger-bg) text-(--tone-danger-fg)' },
}
