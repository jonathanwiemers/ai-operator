// Formatting of lines of code for the Desk's tables (R16): written lines
// (the calls' step commits) and merged lines (the merge requests' final
// diffs, by category). Lines are "+added/−deleted" with a real minus sign;
// a partial merged count is a lower bound, "≥ +A/−D"; an unmeasured one is
// "—", never "+0/−0". Shared by the Issues table, the milestone rows, the
// run rows, the Code section and the Metrics tab.
import type { CompanionLines, LineCount, MergedLines, MergedLineTotals } from './generated/store'

// linesLabel is "+A/−D".
export function linesLabel(added: number, deleted: number): string {
  return `+${added}/−${deleted}`
}

const countLabel = (c: LineCount | undefined) => linesLabel(c?.added ?? 0, c?.deleted ?? 0)

// mergedLabel is the merged production lines: "+A/−D", "≥ +A/−D" when the
// count is partial, "—" when not measured. Tests, docs and generated files
// are in mergedTooltip.
export function mergedLabel(m?: MergedLines): string {
  if (!m) return '—'
  const s = countLabel(m.production)
  return m.partial ? `≥ ${s}` : s
}

// mergedTooltip lists the four categories, then each companion's share of
// the production lines: "production +52/−8, tests +60/−0, docs +10/−0,
// generated +0/−0, group/infra +12/−3". A partial count says so; an
// unmeasured issue says "Merged lines not measured" and still lists its
// companions.
export function mergedTooltip(m: MergedLines | undefined, companions: CompanionLines[] = []): string {
  const parts = m
    ? [
        `production ${countLabel(m.production)}`,
        `tests ${countLabel(m.tests)}`,
        `docs ${countLabel(m.docs)}`,
        `generated ${countLabel(m.generated)}`,
      ]
    : ['Merged lines not measured']
  for (const c of companions) parts.push(`${c.project} ${c.lines ? mergedLabel(c.lines) : 'not measured'}`)
  let s = parts.join(', ')
  if (m?.partial) s += ' (partial: some diffs came without content)'
  return s
}

// measuredLabel is "2 of 3 measured" while some merged issues have no
// merged lines; "" when all are measured or none merged.
export function measuredLabel(t?: Pick<MergedLineTotals, 'merged' | 'measured'>): string {
  if (!t || t.merged === 0 || t.measured >= t.merged) return ''
  return `${t.measured} of ${t.merged} measured`
}

// totalsLabel is mergedLabel of a scope's merged lines: "—" when none of
// its merged issues is measured.
export function totalsLabel(t?: MergedLineTotals): string {
  return t && t.measured > 0 ? mergedLabel(t.lines) : '—'
}

// totalsTooltip is the categories of a scope's merged lines and how many of
// its merged issues are measured.
export function totalsTooltip(t?: MergedLineTotals): string {
  if (!t || t.measured === 0) return t?.merged ? `Merged lines not measured (0 of ${t.merged})` : 'Nothing merged'
  const measured = measuredLabel(t)
  return measured ? `${mergedTooltip(t.lines)} · ${measured}` : mergedTooltip(t.lines)
}

// writtenLabel is the written lines of a spend's calls: "+A/−D", missing
// fields counting 0; "—" without a spend (no calls recorded).
export function writtenLabel(totals?: { linesAdded?: number; linesDeleted?: number }): string {
  if (!totals) return '—'
  return linesLabel(totals.linesAdded ?? 0, totals.linesDeleted ?? 0)
}
