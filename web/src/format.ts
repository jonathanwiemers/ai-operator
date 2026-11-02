import type { CIState, Stage, TaskPhase } from './types'

// safeURL only lets http(s) links through: URLs come from GitLab via the
// cluster, and a javascript: URL must never become a link.
export function safeURL(u: string | undefined | null): string | undefined {
  if (!u) return undefined
  try {
    const parsed = new URL(u)
    return parsed.protocol === 'https:' || parsed.protocol === 'http:' ? parsed.href : undefined
  } catch {
    return undefined
  }
}

export function relTime(iso: string | undefined | null, now: number): string {
  if (!iso) return '—'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return '—'
  const diff = Math.round((t - now) / 1000)
  const abs = Math.abs(diff)
  const units: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, 'second'],
    [3600, 'minute'],
    [86400, 'hour'],
    [86400 * 30, 'day'],
    [86400 * 365, 'month'],
  ]
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
  if (abs < 45) return diff < 0 ? 'just now' : 'in a moment'
  let div = 1
  for (const [limit, unit] of units) {
    if (abs < limit) return rtf.format(Math.round(diff / div), unit)
    div = limit
  }
  return rtf.format(Math.round(diff / (86400 * 365)), 'year')
}

export function absTime(iso: string | undefined | null): string {
  if (!iso) return ''
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? '' : t.toLocaleString()
}

const windowLabels: Record<string, string> = {
  five_hour: '5 h',
  seven_day: '7 days',
  seven_day_opus: '7 days opus',
  seven_day_sonnet: '7 days sonnet',
}

// windowLabel names a usage window, e.g. five_hour → "5 h".
export function windowLabel(name: string): string {
  return windowLabels[name] ?? name.replaceAll('_', ' ')
}

// fullTime is the local date and time with the time zone ("2 Nov 2026,
// 14:59 CET"), for the tooltip of a clockTime.
export function fullTime(iso: string | undefined | null): string {
  if (!iso) return ''
  const t = new Date(iso)
  return Number.isNaN(t.getTime())
    ? ''
    : t.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZoneName: 'short' } as Intl.DateTimeFormatOptions)
}

// clockTime is a local time of day ("14:00"), with the weekday when it is
// not today ("Mon 09:00") and the date when it is not within a week
// ("13 Nov").
export function clockTime(iso: string | undefined | null, now: number): string {
  if (!iso) return ''
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return ''
  const hm = t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  if (t.toDateString() === new Date(now).toDateString()) return hm
  if (Math.abs(t.getTime() - now) < 6 * 86400 * 1000) return `${t.toLocaleDateString(undefined, { weekday: 'short' })} ${hm}`
  return t.toLocaleDateString(undefined, { day: 'numeric', month: 'short' })
}

export function cost(usd: string | undefined | null): string {
  const v = Number.parseFloat(usd ?? '')
  if (!Number.isFinite(v) || v === 0) return '$0'
  return v < 0.01 ? '<$0.01' : `$${v.toFixed(2)}`
}

export function shortSHA(sha: string | undefined | null): string {
  return sha ? sha.slice(0, 8) : '—'
}

export type Tone = 'neutral' | 'info' | 'progress' | 'success' | 'warning' | 'danger' | 'muted'

export const stageInfo: Record<Stage, { label: string; tone: Tone; hint: string }> = {
  pending: { label: 'Pending', tone: 'muted', hint: 'Nothing ran yet.' },
  working: { label: 'Working', tone: 'progress', hint: 'A run is requested or in progress.' },
  waiting: { label: 'Waiting', tone: 'neutral', hint: 'Waits for a free slot or for a usage limit to reset.' },
  'needs-input': { label: 'Needs input', tone: 'warning', hint: 'The agent has questions, or CI is still red after the fix rounds.' },
  ci: { label: 'CI', tone: 'info', hint: 'The pipeline of the last push runs or is being fixed.' },
  review: { label: 'Review', tone: 'success', hint: 'The merge request waits for a human.' },
  planned: { label: 'Planned', tone: 'success', hint: 'The spec agent planned the milestone.' },
  failed: { label: 'Failed', tone: 'danger', hint: 'The last run failed.' },
  blocked: { label: 'Blocked', tone: 'danger', hint: 'The task hit a safety limit and needs a human.' },
}

export const stageOrder: Stage[] = [
  'working',
  'waiting',
  'needs-input',
  'ci',
  'review',
  'failed',
  'blocked',
  'planned',
  'pending',
]

export function phaseTone(p: TaskPhase | undefined): Tone {
  switch (p) {
    case 'Running':
      return 'progress'
    case 'Queued':
    case 'Waiting':
      return 'warning'
    case 'Blocked':
      return 'danger'
    case 'Idle':
      return 'info'
    default:
      return 'muted'
  }
}

export function ciTone(s: CIState | undefined): Tone {
  switch (s) {
    case 'Success':
      return 'success'
    case 'Running':
    case 'Pending':
      return 'info'
    case 'Fixing':
      return 'progress'
    case 'Failed':
      return 'danger'
    case 'BaseFailing':
    case 'Canceled':
      return 'warning'
    default:
      return 'muted'
  }
}

export const ciLabel: Record<CIState, string> = {
  Pending: 'Pending',
  Running: 'Running',
  Fixing: 'Fixing',
  Success: 'Passed',
  Failed: 'Failed',
  BaseFailing: 'Failing on default branch',
  Canceled: 'Canceled',
  None: 'No pipeline',
}

// autopilotWaitingFor returns the issue a paused autopilot waits for: its
// pause ends by itself, and a run is working on that issue now.
export function autopilotWaitingFor(
  ap: { phase?: string; resumeWhenReady?: boolean; pausedFor?: number } | undefined,
  tasks: { issue?: { iid: number }; runPending: boolean }[],
): number {
  if (!ap || ap.phase !== 'Paused' || !ap.resumeWhenReady || !ap.pausedFor) return 0
  return tasks.some((t) => t.issue?.iid === ap.pausedFor && t.runPending) ? ap.pausedFor : 0
}

// duration is a run's length in minutes: "<1 min", "3 min", "1 h 5 min".
export function duration(ms: number): string {
  const min = Math.round(ms / 60000)
  return min < 1 ? '<1 min' : min < 60 ? `${min} min` : `${Math.floor(min / 60)} h ${min % 60} min`
}

// runLine describes a run: "claude/claude-sonnet-5-5 · 3 min · 07:00".
// Parts that are not known (yet) are left out.
export function runLine(
  engine: string | undefined,
  model: string | undefined,
  durationMs: number | undefined,
  start: string | undefined,
): string {
  const parts: string[] = []
  if (engine) parts.push(model ? `${engine}/${model}` : engine)
  if (durationMs !== undefined && durationMs >= 0) parts.push(duration(durationMs))
  const at = clockTime(start, Date.now())
  if (at) parts.push(at)
  return parts.join(' · ')
}
