import type { Tone } from './format.ts'
import type { CompanionView } from './types'

// The companion merge request of an issue (cross-repo changes, decision 12
// of docs/specs/cross-repo-changes.spec.md): its chip on the issue and MR
// cards and in the milestone panel.

const tones: Record<string, Tone> = {
  draft: 'muted',
  'waiting for release': 'info',
  ready: 'warning',
  merging: 'progress',
  merged: 'success',
}

// companionTone is the chip's tone. Blocked and closed companions keep the
// chip of the step they stopped at: blocked turns danger (a human must
// act), closed muted (it ended without a merge). An unknown chip (a newer
// operator) is neutral.
export function companionTone(c: Pick<CompanionView, 'chip' | 'state'>): Tone {
  if (c.state === 'blocked') return 'danger'
  if (c.state === 'closed') return 'muted'
  return tones[c.chip] ?? 'neutral'
}

// companionLabel is the chip's text: the chip, and blocked or closed after
// the step it stopped at ("ready · blocked").
export function companionLabel(c: Pick<CompanionView, 'chip' | 'state'>): string {
  const chip = c.chip || 'companion'
  return c.state === 'blocked' || c.state === 'closed' ? `${chip} · ${c.state}` : chip
}

// companionRef names the companion merge request: "group/infra!12", the
// project alone before its merge request exists.
export function companionRef(c: Pick<CompanionView, 'project' | 'mrIID'>): string {
  return c.mrIID ? `${c.project}!${c.mrIID}` : c.project
}

// companionTitle is the chip's tooltip: the merge request, then why it
// waits, is blocked or was closed (operator text, shown as text only).
export function companionTitle(c: Pick<CompanionView, 'project' | 'mrIID' | 'reason' | 'release'>): string {
  const head = `Companion ${companionRef(c)}`
  const reason = (c.reason || '').trim()
  const release = (c.release || '').trim()
  return [head, reason, release && !reason.includes(release) ? `release ${release}` : ''].filter(Boolean).join(' · ')
}

// companionOf is the companion of the issue with the IID among a
// milestone's companions, undefined without one.
export function companionOf(companions: CompanionView[] | undefined, iid: number | undefined): CompanionView | undefined {
  if (!iid) return undefined
  return (companions || []).find((c) => c.homeIssueIID === iid)
}
