import { cost, tokenCount } from './format.ts'
import type { SpendBreakdown, SpendChild, SpendTotals, SubagentBreakdown } from './types'

// The Spend tab's drill-down (decision 5): project → milestone → issue →
// run → calls. The milestone is the stream's route parameter, the rest
// lives in the query (spendIssue, spendTask, spendRun) next to tab=spend,
// so a position can be linked to and back/forward walks the drill-down.

// SpendScope is what getSpend narrows by; run needs task.
export interface SpendScope {
  milestone?: number
  issue?: number
  task?: string
  run?: number
}

type QueryValue = string | null | (string | null)[] | undefined

const spendParams = ['spendIssue', 'spendTask', 'spendRun'] as const

function first(v: QueryValue): string | undefined {
  const s = Array.isArray(v) ? v[0] : v
  return s ?? undefined
}

function positive(v: QueryValue): number | undefined {
  const s = first(v)
  if (!s || !/^[1-9][0-9]*$/.test(s)) return undefined
  const n = Number(s)
  return Number.isSafeInteger(n) ? n : undefined
}

// A task is an AgentTask name (a DNS subdomain).
const taskName = /^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$/

// scopeFromQuery is the drill-down position of a stream (milestone: its
// route parameter): invalid values are dropped, a run without its task too.
export function scopeFromQuery(milestone: number | undefined, query: Record<string, QueryValue>): SpendScope {
  const s: SpendScope = {}
  if (milestone) s.milestone = milestone
  const issue = positive(query.spendIssue)
  if (issue) s.issue = issue
  const task = first(query.spendTask)
  if (task && taskName.test(task)) {
    s.task = task
    const run = positive(query.spendRun)
    if (run) s.run = run
  }
  return s
}

// spendQuery is the query of a drill-down position on the Spend tab,
// keeping the parameters that are not the drill-down's.
export function spendQuery(query: Record<string, QueryValue>, scope: SpendScope): Record<string, QueryValue> {
  const out: Record<string, QueryValue> = {}
  for (const [k, v] of Object.entries(query)) {
    if (!(spendParams as readonly string[]).includes(k)) out[k] = v
  }
  out.tab = 'spend'
  if (scope.issue) out.spendIssue = String(scope.issue)
  if (scope.task) out.spendTask = scope.task
  if (scope.task && scope.run) out.spendRun = String(scope.run)
  return out
}

// spendLocation is the route of a drill-down position: the milestone
// stream when the scope has a milestone, else the project stream.
export function spendLocation(
  namespace: string,
  name: string,
  scope: SpendScope,
  query: Record<string, QueryValue> = {},
): { name: string; params: Record<string, string>; query: Record<string, QueryValue> } {
  const q = spendQuery(query, scope)
  return scope.milestone
    ? { name: 'stream-milestone', params: { namespace, name, iid: String(scope.milestone) }, query: q }
    : { name: 'stream-project', params: { namespace, name }, query: q }
}

// childScope is where clicking a child drills to, null for the leaves
// (calls) and for children the API cannot narrow to: calls without a
// milestone and a milestone's own calls (spec, pilot, retro). A run's key
// is task/run; run 0 (calls before the task's first run) is the task.
export function childScope(scope: SpendScope, child: Pick<SpendChild, 'level' | 'key'>): SpendScope | null {
  const n = positive(child.key)
  switch (child.level) {
    case 'milestone':
      return !scope.milestone && n ? { milestone: n } : null
    case 'issue':
      return n ? { ...(scope.milestone ? { milestone: scope.milestone } : {}), issue: n } : null
    case 'run': {
      const i = child.key.lastIndexOf('/')
      const task = i > 0 ? child.key.slice(0, i) : ''
      if (!taskName.test(task)) return null
      const { task: _t, run: _r, ...up } = scope
      const run = positive(child.key.slice(i + 1))
      return run ? { ...up, task, run } : { ...up, task }
    }
  }
  return null
}

export interface SpendCrumb {
  label: string
  scope: SpendScope
}

// spendCrumbs are the breadcrumbs from the project down to scope, the last
// one the current position. milestone: the milestone's title, if known.
export function spendCrumbs(scope: SpendScope, names: { project: string; milestone?: string }): SpendCrumb[] {
  const crumbs: SpendCrumb[] = [{ label: names.project, scope: {} }]
  const base: SpendScope = {}
  if (scope.milestone) {
    base.milestone = scope.milestone
    crumbs.push({ label: names.milestone || `%${scope.milestone}`, scope: { ...base } })
  }
  if (scope.issue) {
    base.issue = scope.issue
    crumbs.push({ label: `#${scope.issue}`, scope: { ...base } })
  }
  if (scope.task) {
    base.task = scope.task
    if (scope.run) base.run = scope.run
    crumbs.push({ label: scope.run ? `${scope.task} run ${scope.run}` : scope.task, scope: { ...base } })
  }
  return crumbs
}

// spendTokens sums all tokens: input, output and cache.
export function spendTokens(t: SpendTotals): number {
  return t.inputTokens + t.outputTokens + t.cacheReadTokens + t.cacheWriteTokens
}

function usd(t: SpendTotals): number {
  const v = Number.parseFloat(t.costUSD)
  return t.costKnown && Number.isFinite(v) ? v : 0
}

export type ShareBasis = 'cost' | 'tokens'

// shares are the rows' percentages of their sum, by cost when any row has
// a known cost above zero, else by tokens. In cost terms a row without a
// known cost is null (n/a), not 0 %: its calls count no USD.
export function shares(rows: SpendTotals[]): { basis: ShareBasis; pct: (number | null)[] } {
  const costSum = rows.reduce((s, r) => s + usd(r), 0)
  if (costSum > 0) {
    return { basis: 'cost', pct: rows.map((r) => (r.costKnown ? clampPct((usd(r) / costSum) * 100) : null)) }
  }
  const tokenSum = rows.reduce((s, r) => s + spendTokens(r), 0)
  return { basis: 'tokens', pct: rows.map((r) => (tokenSum > 0 ? clampPct((spendTokens(r) / tokenSum) * 100) : 0)) }
}

function clampPct(v: number): number {
  return Number.isFinite(v) ? Math.min(100, Math.max(0, v)) : 0
}

// pctLabel shows a share: "42%", "<1%" for a small one, "n/a" unknown.
export function pctLabel(p: number | null): string {
  if (p === null) return 'n/a'
  if (p > 0 && p < 1) return '<1%'
  return `${Math.round(p)}%`
}

// sortByCost orders rows by known cost, highest first, then the ones
// without a known cost; ties by tokens, then by key.
export function sortByCost<T extends { key: string; totals: SpendTotals }>(rows: T[]): T[] {
  return [...rows].sort(
    (a, b) =>
      Number(b.totals.costKnown) - Number(a.totals.costKnown) ||
      usd(b.totals) - usd(a.totals) ||
      spendTokens(b.totals) - spendTokens(a.totals) ||
      a.key.localeCompare(b.key),
  )
}

// spendUSD is a total's cost: "n/a" when no call reports one, never $0.
export function spendUSD(t: SpendTotals): string {
  return t.costKnown ? cost(t.costUSD) : 'n/a'
}

// callCost is a call's cost by its source: "$1.23" reported, "~$1.23"
// estimated from tokens and pricing, "n/a" unknown.
export function callCost(c: Pick<SpendChild, 'totals' | 'costSource'>): string {
  if (c.costSource === 'unknown' || !c.totals.costKnown) return 'n/a'
  return c.costSource === 'estimated' ? `~${cost(c.totals.costUSD)}` : cost(c.totals.costUSD)
}

// costSourceLabel names where a call's cost comes from.
export function costSourceLabel(src: SpendChild['costSource']): string {
  return src === 'reported' || src === 'estimated' ? src : 'n/a'
}

// unknownNote: "+3 calls without cost", empty when every call has one.
export function unknownNote(t: SpendTotals): string {
  if (t.unknownCalls <= 0) return ''
  return `+${t.unknownCalls} ${t.unknownCalls === 1 ? 'call' : 'calls'} without cost`
}

// legacyNote: the part of the cost from runs before agent calls were
// recorded, which have no tokens.
export function legacyNote(t: SpendTotals): string {
  return t.legacyCostUSD ? `includes ${cost(t.legacyCostUSD)} from runs before token tracking` : ''
}

// shareLabel shows a 0…1 share as a percentage: "42%".
export function shareLabel(v: number): string {
  return pctLabel(Number.isFinite(v) ? clampPct(v * 100) : 0)
}

// modelsLine names a child's models, the main ones first, subagent models
// marked: "claude-opus-5-5, claude-haiku-5-5 (subagent)".
export function modelsLine(models: SpendChild['models']): string {
  return modelOrder(models)
    .map((m) => (m.main ? m.model : `${m.model} (subagent)`))
    .join(', ')
}

// childLabel is a child's row label: a milestone's title when known, and
// a milestone's own calls (spec, pilot, retro) among its issues as such.
export function childLabel(
  c: Pick<SpendChild, 'level' | 'key' | 'label'>,
  scope: SpendScope,
  milestones: { iid: number; title: string }[],
): string {
  if (c.level !== 'milestone' || !c.key) return c.label
  if (scope.milestone) return 'Milestone (spec, pilot, retro)'
  return milestones.find((m) => String(m.iid) === c.key)?.title || c.label
}

// tokenBreakdown is the tooltip of a token count: all four token types.
export function tokenBreakdown(t: SpendTotals): string {
  const n = (v: number) => v.toLocaleString('en-US')
  return `Input ${n(t.inputTokens)} · output ${n(t.outputTokens)}\nCache read ${n(t.cacheReadTokens)} · cache write ${n(t.cacheWriteTokens)}`
}

// runNumber is the run of a run child (key "task/run"), 0 when unknown.
export function runNumber(c: Pick<SpendChild, 'key'>): number {
  return positive(c.key.slice(c.key.lastIndexOf('/') + 1)) ?? 0
}

// runsNewestFirst orders a task's run children by run number, highest first.
export function runsNewestFirst<T extends Pick<SpendChild, 'key'>>(runs: T[]): T[] {
  return [...runs].sort((a, b) => runNumber(b) - runNumber(a))
}

// spendHidden: the call table is left out when research is off, the
// endpoint answers 404 (NotFoundError, "not found") or 503.
export function spendHidden(e: unknown): boolean {
  return e instanceof Error && /^not found$|\b503\b/.test(e.message)
}

// The per-model bars of the drill-down rows (R21): one stacked bar per
// row, the main model first, the subagent models after it. The unit is
// kept in ?spendUnit= (tokens unless "usd"), across the drill-down.
export type SpendUnit = 'tokens' | 'usd'

export function unitFromQuery(query: Record<string, QueryValue>): SpendUnit {
  return first(query.spendUnit) === 'usd' ? 'usd' : 'tokens'
}

// unitQuery is the query with the unit switched, tokens the default.
export function unitQuery(query: Record<string, QueryValue>, unit: SpendUnit): Record<string, QueryValue> {
  const { spendUnit: _u, ...out } = query
  return unit === 'usd' ? { ...out, spendUnit: 'usd' } : out
}

type Split = SpendChild['models'][number]

// modelOrder: the main models first, then by tokens, then by name.
export function modelOrder(models: Split[]): Split[] {
  return [...models].sort(
    (a, b) => Number(b.main) - Number(a.main) || b.tokens - a.tokens || a.model.localeCompare(b.model),
  )
}

// The colors of the bars: slot 0 the main models, 1 and 2 the first two
// subagent models of a table by name, 3 any further ones.
export const barColors = ['var(--tone-progress-fg)', 'var(--accent-2)', 'var(--tone-info-fg)', 'var(--text-3)'] as const

// modelSlots gives each subagent model of a table its color slot, by name,
// so a model keeps its color in every row.
export function modelSlots(rows: Pick<SpendChild, 'models'>[]): Map<string, number> {
  const names = [...new Set(rows.flatMap((r) => r.models.filter((m) => !m.main).map((m) => m.model)))].sort()
  return new Map(names.map((n, i) => [n, Math.min(i + 1, barColors.length - 1)]))
}

// splitPct is each value's percentage of their sum in tenths, adding up to
// exactly 100 (largest remainder); a value above zero keeps at least 0.1.
export function splitPct(values: number[]): number[] {
  const v = values.map((x) => (Number.isFinite(x) && x > 0 ? x : 0))
  const sum = v.reduce((s, x) => s + x, 0)
  if (sum <= 0) return v.map(() => 0)
  const parts = v.map((x, i) => {
    const exact = (x / sum) * 1000
    const units = x > 0 ? Math.max(1, Math.floor(exact)) : 0
    return { i, positive: x > 0, rest: exact - units, units }
  })
  let left = 1000 - parts.reduce((s, p) => s + p.units, 0)
  // Hand out the missing tenths by largest remainder (more than one round
  // only when the minimum of 0.1 took some), take extra ones from the largest.
  const byRest = parts.filter((p) => p.positive).sort((a, b) => b.rest - a.rest || a.i - b.i)
  for (let k = 0; left > 0; k = (k + 1) % byRest.length, left--) byRest[k]!.units++
  for (; left < 0; left++) parts.reduce((m, p) => (p.units > m.units ? p : m)).units--
  return parts.map((p) => p.units / 10)
}

export interface BarSegment {
  model: string
  main: boolean
  slot: number
  // x and width in percent of the full bar width (the table's largest row).
  x: number
  width: number
  // share: the segment's percentage of its own bar.
  share: number
  title: string
}

export interface ModelBarData {
  segments: BarSegment[]
  // width of the whole bar in percent of the full width.
  width: number
  label: string
  // fallback: USD was asked for but the row has no known cost, so the bar
  // is of its tokens.
  fallback: boolean
  title: string
}

type BarRow = Pick<SpendChild, 'models' | 'totals' | 'costSource'>

function modelUSD(m: Split): number {
  const v = Number.parseFloat(m.costUSD)
  return m.costKnown && Number.isFinite(v) && v > 0 ? v : 0
}

// usdBar: whether a row draws in USD: a known cost above zero, never $0.
function usdBar(r: BarRow): boolean {
  return r.costSource !== 'unknown' && r.totals.costKnown && r.models.some((m) => modelUSD(m) > 0)
}

// modelBars are the bars of one table's rows. Bars of the same unit share
// a scale, the largest row full width; in USD mode a row without a known
// cost falls back to its tokens (on the tokens' scale), labelled "n/a".
// An estimated cost (a call's costSource) is marked "~".
export function modelBars(rows: BarRow[], unit: SpendUnit): ModelBarData[] {
  const slots = modelSlots(rows)
  const inUSD = rows.map((r) => unit === 'usd' && usdBar(r))
  const tokenSum = (r: BarRow) => r.models.reduce((s, m) => s + Math.max(0, m.tokens), 0)
  const usdSum = (r: BarRow) => r.models.reduce((s, m) => s + modelUSD(m), 0)
  const maxTokens = Math.max(0, ...rows.map((r, i) => (inUSD[i] ? 0 : tokenSum(r))))
  const maxUSD = Math.max(0, ...rows.map((r, i) => (inUSD[i] ? usdSum(r) : 0)))
  return rows.map((r, i) => {
    const usd = inUSD[i]
    const tilde = r.costSource === 'estimated' ? '~' : ''
    const money = (m: Split) => (m.costKnown ? `${tilde}${cost(m.costUSD)}` : 'n/a')
    const models = modelOrder(r.models).filter((m) => (usd ? modelUSD(m) : m.tokens) > 0)
    const values = models.map((m) => (usd ? modelUSD(m) : m.tokens))
    const total = usd ? usdSum(r) : tokenSum(r)
    const max = usd ? maxUSD : maxTokens
    const width = max > 0 ? clampPct((total / max) * 100) : 0
    const shares = splitPct(values)
    let x = 0
    const segments = models.map((m, k) => {
      const w = ((shares[k] ?? 0) * width) / 100
      const seg: BarSegment = {
        model: m.model,
        main: m.main,
        slot: m.main ? 0 : (slots.get(m.model) ?? barColors.length - 1),
        x,
        width: w,
        share: shares[k] ?? 0,
        title: `${m.model}${m.main ? '' : ' (subagent)'}\n${m.tokens.toLocaleString('en-US')} tokens · ${money(m)}`,
      }
      x += w
      return seg
    })
    const fallback = unit === 'usd' && !usd
    const label = fallback ? 'n/a' : usd ? `${tilde}${cost(String(total))}` : total > 0 ? tokenCount(total) : '—'
    const omitted = usd ? r.models.filter((m) => !modelUSD(m)).map((m) => m.model) : []
    const title = [
      fallback ? 'No known cost: the bar shows tokens' : '',
      omitted.length ? `Without a known cost, not in the bar: ${omitted.join(', ')}` : '',
    ]
      .filter(Boolean)
      .join('\n')
    return { segments, width, label, fallback, title }
  })
}

// The tuning tables (R9, decisions 8 and 9): review cost per round,
// implementer steps fresh vs resumed, calls with vs without subagents.
// They compare per call, so scopes of different sizes compare.

// avgCost is the average cost of a total's calls with a known cost: "n/a"
// when none has one, never $0.
export function avgCost(t: SpendTotals): string {
  const known = t.calls - Math.max(0, t.unknownCalls)
  if (!t.costKnown || known <= 0) return 'n/a'
  return cost(String(usd(t) / known))
}

// avgTokens is the average token count of a total's calls, 0 without calls.
export function avgTokens(t: SpendTotals): number {
  return t.calls > 0 ? spendTokens(t) / t.calls : 0
}

// reviewRounds orders the review rounds by number: "1", "2", … "10".
export function reviewRounds(rows: SpendBreakdown[]): SpendBreakdown[] {
  return [...rows].sort((a, b) => Number(a.key) - Number(b.key))
}

// sessionRows are the implement calls by session, fresh first.
export function sessionRows(rows: SpendBreakdown[]): SpendBreakdown[] {
  const order = ['fresh', 'resumed']
  return [...rows].sort((a, b) => order.indexOf(a.key) - order.indexOf(b.key))
}

export interface SubagentRole {
  role: string
  // rows: the calls with subagents, then (if any) those without.
  rows: SubagentBreakdown[]
}

// subagentRoles pairs each role's calls with and without subagents, the
// roles where a subagent ran only: without one there is nothing to compare.
export function subagentRoles(rows: SubagentBreakdown[]): SubagentRole[] {
  const roles = [...new Set(rows.filter((r) => r.withSubagents && r.totals.calls > 0).map((r) => r.role))].sort()
  return roles.map((role) => ({
    role,
    rows: rows.filter((r) => r.role === role).sort((a, b) => Number(b.withSubagents) - Number(a.withSubagents)),
  }))
}

// gateBlocksPerCall is the read gate's denials per call with one decimal:
// "1.5", "" without calls or without a block.
export function gateBlocksPerCall(r: SubagentBreakdown): string {
  return r.totals.calls > 0 && r.readGateBlocks > 0 ? (r.readGateBlocks / r.totals.calls).toFixed(1) : ''
}

// subagentShare is the subagent models' share of a row's tokens: "23%",
// "—" without tokens.
export function subagentShare(r: SubagentBreakdown): string {
  const all = spendTokens(r.totals)
  return all > 0 ? shareLabel(r.subagentTokens / all) : '—'
}

// companionNote is the tooltip of an issue row's companion marker: the
// companion project whose changes its calls made too (they count to the
// home issue); '' for other rows and issues without a companion.
export function companionNote(c: Pick<SpendChild, 'level' | 'companion'>): string {
  const project = (c.companion || '').trim()
  if (c.level !== 'issue' || !project) return ''
  return `Also changes ${project}: its calls count to this issue`
}
