import { costChip } from './format.ts'
import type { OverviewView, UsageLimit } from './types'

// StripChip is one figure of the dashboard's summary strip (decision 4).
// inbox: the chip links to the Inbox.
export interface StripChip {
  id: 'running' | 'paused' | 'waiting' | 'needsYou' | 'spendToday' | 'spend7d'
  value: string
  label: string
  inbox?: boolean
  // warn: a count that asks for someone (paused, needs you) above zero.
  warn?: boolean
}

// overviewStrip formats the strip: the milestone counts, the needs-you
// count and the spend of today and the last 7 days. A spend the server did
// not send (no history database) is left out: unknown, not $0.
export function overviewStrip(o: OverviewView): StripChip[] {
  const chips: StripChip[] = [
    { id: 'running', value: String(o.running), label: 'running' },
    { id: 'paused', value: String(o.paused), label: 'paused', warn: o.paused > 0 },
    { id: 'waiting', value: String(o.waiting), label: 'waiting' },
    { id: 'needsYou', value: String(o.needsYou), label: 'need you', inbox: true, warn: o.needsYou > 0 },
  ]
  if (o.spendToday) chips.push({ id: 'spendToday', value: costChip(o.spendToday), label: 'today' })
  if (o.spend7d) chips.push({ id: 'spend7d', value: costChip(o.spend7d), label: 'last 7 days' })
  return chips
}

// activeLimits are the usage limits still in force at now, soonest end
// first.
export function activeLimits(limits: UsageLimit[] | undefined, now: number): UsageLimit[] {
  return (limits ?? [])
    .filter((l) => Date.parse(l.until) > now)
    .sort((a, b) => Date.parse(a.until) - Date.parse(b.until))
}
