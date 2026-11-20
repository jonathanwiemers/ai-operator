// Cron schedules in words (cronText) for the rail and the research pages.
// Its own module, not format.ts: format.ts is in the initial bundle, and
// only the lazy rail and research chunks need this.

const cronDays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

// cronInt reads a whole number from lo to hi, null for anything else.
function cronInt(s: string, lo: number, hi: number): number | null {
  if (!/^\d{1,2}$/.test(s)) return null
  const n = Number(s)
  return n >= lo && n <= hi ? n : null
}

// cronDay reads a day of the week: 0–7 (0 and 7 are Sunday) or its
// three-letter name.
function cronDay(s: string): number | null {
  const i = cronDays.findIndex((d) => d.toLowerCase() === s.toLowerCase())
  if (i >= 0) return i
  const n = cronInt(s, 0, 7)
  return n === null ? null : n % 7
}

// cronDaySet reads a day-of-week field of single days and ranges ("1-5",
// "0,6", "MON,WED"), null for steps and anything else.
function cronDaySet(field: string): Set<number> | null {
  const days = new Set<number>()
  for (const part of field.split(',')) {
    const [a, b, ...more] = part.split('-')
    if (more.length || a === undefined) return null
    const from = cronDay(a)
    // 7 ends a range as Sunday after Saturday: 5-7 is Fri–Sun.
    const to = b === undefined ? from : b === '7' ? 7 : cronDay(b)
    if (from === null || to === null || to < from) return null
    for (let d = from; d <= to; d++) days.add(d % 7)
  }
  return days
}

// cronDayText names a set of days: "weekdays", "weekends", runs of three
// or more as a range ("Mon–Thu"), others listed ("Mon, Wed").
function cronDayText(days: Set<number>): string {
  const key = [...days].sort((a, b) => a - b).join(',')
  if (key === '1,2,3,4,5') return 'weekdays'
  if (key === '0,6') return 'weekends'
  const sorted = key.split(',').map(Number)
  const parts: string[] = []
  for (let i = 0; i < sorted.length; ) {
    let j = i
    while (j + 1 < sorted.length && sorted[j + 1] === sorted[j]! + 1) j++
    if (j - i >= 2) parts.push(`${cronDays[sorted[i]!]}–${cronDays[sorted[j]!]}`)
    else for (let k = i; k <= j; k++) parts.push(cronDays[sorted[k]!]!)
    i = j + 1
  }
  return parts.join(', ')
}

// cronText is a cron schedule in words with its time zone (UTC when none
// is set): "daily at 07:15 Europe/Berlin", "weekdays at 09:00 UTC",
// "Mon, Wed at 09:00 UTC". Intervals have no zone: "every 15 minutes",
// "every 2 hours". Any other form (a day of the month, a month, lists or
// ranges of hours, @daily macros) is null: the caller shows the raw cron.
export function cronText(expr: string | undefined | null, timeZone?: string | null): string | null {
  const f = (expr ?? '').trim().split(/\s+/)
  if (f.length !== 5) return null
  const [min, hour, dom, month, dow] = f as [string, string, string, string, string]
  if (dom !== '*' || month !== '*') return null
  const every = (n: number, unit: string) => (n === 1 ? `every ${unit}` : `every ${n} ${unit}s`)
  if (hour === '*' && dow === '*' && (min === '*' || min.startsWith('*/'))) {
    const n = min === '*' ? 1 : cronInt(min.slice(2), 1, 59)
    return n === null ? null : every(n, 'minute')
  }
  if (min === '0' && dow === '*' && (hour === '*' || hour.startsWith('*/'))) {
    const n = hour === '*' ? 1 : cronInt(hour.slice(2), 1, 23)
    return n === null ? null : every(n, 'hour')
  }
  const m = cronInt(min, 0, 59)
  const h = cronInt(hour, 0, 23)
  if (m === null || h === null) return null
  const days = dow === '*' ? null : cronDaySet(dow)
  if (dow !== '*' && !days) return null
  const when = !days || days.size === 7 ? 'daily' : cronDayText(days)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${when} at ${pad(h)}:${pad(m)} ${timeZone || 'UTC'}`
}

// cronRaw is the raw schedule with its time zone, for the tooltip next to
// cronText: "15 7 * * * Europe/Berlin".
export function cronRaw(expr: string, timeZone?: string | null): string {
  return `${expr} ${timeZone || 'UTC'}`
}
