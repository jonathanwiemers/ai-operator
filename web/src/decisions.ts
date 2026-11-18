// The Decisions tab (R11, R12): grouping a milestone's or project's stored
// decisions by issue, the card's option list, and the tab's URL state
// (?q= search, ?state= chips) and the API query it loads.
import type { DecisionIssueView, DecisionView } from './generated/views'

export type DecisionState = 'all' | 'active' | 'superseded'

export const decisionStates: { id: DecisionState; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'active', label: 'Active' },
  { id: 'superseded', label: 'Superseded' },
]

type QueryValue = string | null | (string | null)[] | undefined

function first(v: QueryValue): string {
  const s = Array.isArray(v) ? v[0] : v
  return s ?? ''
}

// stateFromQuery reads ?state=: active or superseded, anything else All.
export function stateFromQuery(v: QueryValue): DecisionState {
  const s = first(v)
  return s === 'active' || s === 'superseded' ? s : 'all'
}

// searchFromQuery reads ?q=, trimmed.
export function searchFromQuery(v: QueryValue): string {
  return first(v).trim()
}

// decisionsQuery is the route query with the search and state patched in,
// keeping the other parameters (the tab): an empty search and All drop
// theirs.
export function decisionsQuery(
  query: Record<string, QueryValue>,
  patch: { q?: string; state?: DecisionState },
): Record<string, QueryValue> {
  const { q: _q, state: _s, ...rest } = query
  const q = patch.q !== undefined ? patch.q.trim() : searchFromQuery(query.q)
  const state = patch.state ?? stateFromQuery(query.state)
  return { ...rest, ...(q ? { q } : {}), ...(state !== 'all' ? { state } : {}) }
}

// DecisionParams is what GET …/decisions filters on: milestone 0 is the
// whole project.
export interface DecisionParams {
  milestone: number
  q: string
  state: DecisionState
}

// decisionsSearch is the API query string of params, "" when nothing
// filters.
export function decisionsSearch(p: DecisionParams): string {
  const s = new URLSearchParams()
  if (p.milestone > 0) s.set('milestone', String(p.milestone))
  if (p.q) s.set('q', p.q)
  if (p.state !== 'all') s.set('state', p.state)
  const qs = s.toString()
  return qs ? `?${qs}` : ''
}

// filtered: a search or a state chip is active, so an empty list says
// nothing matched rather than nothing was recorded.
export function filtered(p: Pick<DecisionParams, 'q' | 'state'>): boolean {
  return p.q !== '' || p.state !== 'all'
}

export interface DecisionGroup {
  key: string
  milestoneIID: number
  issue: DecisionIssueView
  decisions: DecisionView[]
}

// groupByIssue groups decisions by their issue, the groups and the
// decisions in each in the order the server returns them (plan order, then
// ID). Issues of different milestones never share a group.
export function groupByIssue(decisions: readonly DecisionView[]): DecisionGroup[] {
  const groups: DecisionGroup[] = []
  const byKey = new Map<string, DecisionGroup>()
  for (const d of decisions) {
    const key = `${d.milestoneIID}/${d.issue.iid}`
    let g = byKey.get(key)
    if (!g) {
      g = { key, milestoneIID: d.milestoneIID, issue: d.issue, decisions: [] }
      byKey.set(key, g)
      groups.push(g)
    }
    g.decisions.push(d)
  }
  return groups
}

export interface DecisionOption {
  text: string
  chosen: boolean
}

// optionsOf is a card's option list: the choice first, then the
// alternatives; blank ones are left out.
export function optionsOf(d: Pick<DecisionView, 'choice' | 'alternatives'>): DecisionOption[] {
  const out: DecisionOption[] = []
  if (d.choice.trim()) out.push({ text: d.choice, chosen: true })
  for (const a of d.alternatives ?? []) {
    if (a.trim()) out.push({ text: a, chosen: false })
  }
  return out
}

// kindLabel is the card's chip: Plan for the planner, Implementation for
// the implementer.
export function kindLabel(kind: DecisionView['kind']): string {
  return kind === 'planner' ? 'Plan' : 'Implementation'
}

// issueStateLabel is an issue group's state chip: open or closed.
export function issueStateLabel(state: string): string {
  return state === 'opened' ? 'open' : state
}

// decisionFromHash is the decision ID a route hash names (#D42-3), "" for
// none or a hash that does not decode.
export function decisionFromHash(hash: string): string {
  if (!hash.startsWith('#') || hash.length < 2) return ''
  try {
    return decodeURIComponent(hash.slice(1))
  } catch {
    return ''
  }
}
