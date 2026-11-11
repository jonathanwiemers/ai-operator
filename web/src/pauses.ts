import type { PauseHistory, Pause } from './types'

// The Pauses card of the milestone context panel: the autopilot's pause
// history (MilestoneView.pauses) with its totals. The server counts an open
// pause up to the view's time; pausesSummary counts it on up to now.

export interface PauseKindRow {
  kind: string
  count: number
  sec: number
}

export interface PauseResolverRow {
  resolver: string
  label: string
  count: number
}

export interface PauseViaRow {
  via: string
  label: string
  count: number
}

export interface PauseRow {
  kind: string
  reason: string
  item: string
  duration: string
  resolver: string
  // via is how it ended ("" while open or not recorded).
  via: string
  open: boolean
  since: string
}

export interface PausesSummary {
  count: number
  pausedSec: number
  // meanResumeSec is absent while no pause has ended.
  meanResumeSec?: number
  kinds: PauseKindRow[]
  resolvers: PauseResolverRow[]
  vias: PauseViaRow[]
  rows: PauseRow[]
}

const resolverLabels = new Map<string, string>(Object.entries({
  operator: 'operator',
  pilot: 'pilot',
  owner: 'owner',
  selfHealed: 'self-healed',
  laterRun: 'later run',
}))

// resolverLabel names who ended a pause.
export function resolverLabel(r: string | undefined): string {
  if (!r) return 'unknown'
  return resolverLabels.get(r) ?? r
}

const viaLabels = new Map<string, string>(Object.entries({
  reReview: 're-review',
  takeover: 'takeover',
  push: 'push',
  mergedByHand: 'merged by hand',
  closedByHand: 'closed by hand',
  command: 'command',
  causeGone: 'cause gone',
  selfHealed: 'self-healed',
}))

// viaLabel names how a pause ended, "" when that is not recorded.
export function viaLabel(v: string | undefined): string {
  if (!v) return ''
  return viaLabels.get(v) ?? v
}

// pauseDuration is a pause's length: "<1m", "47m", "3h 10m", "2d 4h".
export function pauseDuration(sec: number): string {
  const min = Math.floor(Math.max(0, sec) / 60)
  if (min < 1) return '<1m'
  if (min < 60) return `${min}m`
  const h = Math.floor(min / 60)
  if (h < 24) return min % 60 ? `${h}h ${min % 60}m` : `${h}h`
  return h % 24 ? `${Math.floor(h / 24)}d ${h % 24}h` : `${Math.floor(h / 24)}d`
}

// liveExtra is how much longer an open pause has lasted since the view was
// made: its seconds count up to the view's time.
function liveExtra(p: Pause, now: number): number {
  if (p.until) return 0
  const since = Date.parse(p.since)
  if (Number.isNaN(since)) return 0
  return Math.max(0, Math.floor((now - since) / 1000) - p.seconds)
}

// oneLine is the reason as one line of text (it is untrusted and shown as
// text, never as HTML).
function oneLine(s: string | undefined): string {
  return (s ?? '').replace(/\s+/g, ' ').trim()
}

// pausesSummary is what the Pauses card shows, undefined without pauses:
// the totals (open pauses up to now), the kinds by time paused, the
// resolvers and the ways they ended by count, and the newest pauses as list
// rows.
export function pausesSummary(h: PauseHistory | undefined | null, now: number): PausesSummary | undefined {
  if (!h || !h.totals || !h.totals.count) return undefined
  const pauses = h.pauses ?? []
  const byKind = new Map<string, PauseKindRow>()
  for (const [kind, t] of Object.entries(h.totals.byKind ?? {})) {
    byKind.set(kind, { kind, count: t.count, sec: t.sec })
  }
  let pausedSec = h.totals.pausedSec
  const rows: PauseRow[] = pauses.map((p) => {
    const extra = liveExtra(p, now)
    pausedSec += extra
    if (extra) {
      const k = byKind.get(p.kind) ?? { kind: p.kind, count: 1, sec: 0 }
      k.sec += extra
      byKind.set(p.kind, k)
    }
    return {
      kind: p.kind,
      reason: oneLine(p.reason),
      item: p.item ? `#${p.item}` : '',
      duration: pauseDuration(p.seconds + extra),
      resolver: p.until ? resolverLabel(p.resolvedBy) : 'open',
      via: p.until ? viaLabel(p.via) : '',
      open: !p.until,
      since: p.since,
    }
  })
  const kinds = [...byKind.values()].sort(
    (a, b) => b.sec - a.sec || b.count - a.count || a.kind.localeCompare(b.kind),
  )
  const resolvers = Object.entries(h.totals.byResolver ?? {})
    .filter(([, n]) => n > 0)
    .map(([resolver, count]) => ({ resolver, label: resolverLabel(resolver === 'unknown' ? '' : resolver), count }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
  const vias = Object.entries(h.totals.byVia ?? {})
    .filter(([, n]) => n > 0)
    .map(([via, count]) => ({ via, label: via === 'unknown' ? 'unknown' : viaLabel(via), count }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
  const ended = resolvers.reduce((n, r) => n + r.count, 0)
  return {
    count: h.totals.count,
    pausedSec,
    meanResumeSec: ended ? h.totals.meanResumeSec : undefined,
    kinds,
    resolvers,
    vias,
    rows,
  }
}

// pausesLine is the card's totals line: "paused 3h 10m in 4 pauses · mean
// 47m to resume".
export function pausesLine(s: PausesSummary): string {
  const n = s.count === 1 ? '1 pause' : `${s.count} pauses`
  const mean = s.meanResumeSec !== undefined ? ` · mean ${pauseDuration(s.meanResumeSec)} to resume` : ''
  return `paused ${pauseDuration(s.pausedSec)} in ${n}${mean}`
}
