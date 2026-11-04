import { cost } from './format.ts'
import type { SpendChild, SpendTotals } from './types'

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
  return [...models]
    .sort((a, b) => Number(b.main) - Number(a.main) || b.tokens - a.tokens)
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
