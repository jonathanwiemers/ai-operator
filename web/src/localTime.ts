import { h, type VNodeChild } from 'vue'
import { fullTime, relTime } from './format.ts'

// Server text writes times in UTC, as "2026-11-21 14:05 UTC" (Go's
// timefmt.UTCMinute) or with seconds. The Desk shows each such time in the
// browser's zone, with the time as written in its tooltip. Times without a
// date, other zone names and ISO forms ("2026-11-21T14:05:00Z") stay as
// written.

// LocalTimePart is a piece of text, or a UTC time found in it.
export type LocalTimePart = { text: string } | { iso: string; label: string; title: string; written: string }

// Not after a letter, digit or _ (a group, not a lookbehind, which older
// Safari cannot parse), and "UTC" ends at a word boundary.
const utcRe = /(^|[^\p{L}\p{N}_])(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2})(:\d{2})? UTC\b/gu

// utcInstant is the ISO instant of a matched date and time, or null when
// they are no real date and time (month 13, 30 February, 24:00).
function utcInstant(date: string, hm: string, sec: string | undefined): string | null {
  const iso = `${date}T${hm}${sec ?? ':00'}.000Z`
  const t = Date.parse(iso)
  return !Number.isNaN(t) && new Date(t).toISOString() === iso ? iso : null
}

// localTimes splits text into text parts and the UTC times in it.
export function localTimes(text: string, now: number): LocalTimePart[] {
  const out: LocalTimePart[] = []
  let from = 0
  for (const m of text.matchAll(utcRe)) {
    const iso = utcInstant(m[2]!, m[3]!, m[4])
    if (!iso) continue
    const at = m.index + m[1]!.length
    if (at > from) out.push({ text: text.slice(from, at) })
    const written = m[0].slice(m[1]!.length)
    out.push({ iso, label: localMinute(iso, now), title: `${fullTime(iso)} · ${written} as written`, written })
    from = at + written.length
  }
  if (from < text.length) out.push({ text: text.slice(from) })
  return out
}

// localTimesText is text with each UTC time replaced by its local label, for
// attributes, toasts and titles.
export function localTimesText(text: string, now: number): string {
  return localTimes(text, now)
    .map((p) => ('text' in p ? p.text : p.label))
    .join('')
}

// localTimeNodes renders text with each UTC time as a <time> element: the
// local label, the instant in datetime and the full times in its title.
export function localTimeNodes(text: string, now: number): VNodeChild[] {
  return localTimes(text, now).map((p) => ('text' in p ? p.text : h('time', { datetime: p.iso, title: p.title }, p.label)))
}

// localDay numbers the local calendar day of t, so that days compare by date
// and not by 24-hour steps, which a DST change would shift.
const localDay = (t: Date) => Date.UTC(t.getFullYear(), t.getMonth(), t.getDate()) / 86400000

// localMinute is the local label of an ISO time at minute precision: "15:05"
// on now's day, "Tue 15:05" within 6 days before or after it, "13 Nov,
// 15:05" in its year and "13 Nov 2027, 15:05" in another; '' when iso is no
// time.
export function localMinute(iso: string, now: number): string {
  const t = new Date(iso)
  if (Number.isNaN(t.getTime())) return ''
  const n = new Date(now)
  const clock = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(t)
  const days = Math.abs(localDay(t) - localDay(n))
  if (days === 0) return clock
  if (days <= 6) return `${new Intl.DateTimeFormat(undefined, { weekday: 'short' }).format(t)} ${clock}`
  const date = new Intl.DateTimeFormat(undefined, {
    day: 'numeric',
    month: 'short',
    year: t.getFullYear() === n.getFullYear() ? undefined : 'numeric',
  }).format(t)
  return `${date}, ${clock}`
}

// nextRunTitle is the tooltip of a research's next run, whose label is its
// localMinute: the full local time and how far off it is ("24 Nov 2026,
// 08:15 CET · in 3 hours").
export function nextRunTitle(iso: string, now: number): string {
  return `${fullTime(iso)} · ${relTime(iso, now)}`
}
