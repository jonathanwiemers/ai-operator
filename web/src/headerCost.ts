// The cost in a project or milestone page's header (Decision 5 of "Desk: one
// header and one row of tabs on every page"): one number per page, the same
// on every tab. Two requests carry it: the project (or milestone) view,
// reloaded on live events, and the Metrics tab's totals of the same scope.
// The server sums both from agent_calls, but it records calls up to a
// second after the task event that reloads the project, and plan calls send
// no event at all, so the project's copy can lag behind the Metrics tile.
// The page shows the larger of the two (keepCost). When the history query
// fails, the server falls back to the live tasks' sum (costFrom 'tasks', no
// tokens), which never replaces a cost from calls.
import type { Cost, MilestoneView, ProjectDetail, SpendTotals } from './types'

// headerCost is the scope's cost from the views: the project's on a project
// page; on a milestone page the loaded milestone's, else the project list's
// copy while it loads, through keepCost: a tasks-based milestone cost gives
// way to a calls-based list copy, a lagging one to the larger.
export function headerCost(
  project: Pick<ProjectDetail, keyof Cost | 'milestones'> | null,
  milestone: (Cost & Pick<MilestoneView, 'iid'>) | null,
  iid: number | undefined,
): Cost | null {
  if (!iid) return project
  const listed = project?.milestones.find((m) => m.iid === iid) ?? null
  const loaded = milestone?.iid === iid ? milestone : null
  return keepCost(listed, loaded)
}

// totalsCost is the Metrics tab's totals as a cost chip, as the server's
// callsCost makes it.
export function totalsCost(t: SpendTotals): Cost {
  return {
    costUSD: t.costUSD,
    costKnown: t.costKnown,
    tokens: t.inputTokens + t.outputTokens + t.cacheReadTokens + t.cacheWriteTokens,
    costFrom: 'calls',
    spend: t,
  }
}

// keepCost is the cost to show when next arrives for the scope prev was
// shown for. A tasks-based sum never replaces one from calls. Of two sums
// from calls the larger stays: calls only add up while a page is open, so
// the smaller one is a copy that lags (the project's, reloaded before the
// server recorded the last calls), whichever request answered last.
export function keepCost(prev: Cost | null, next: Cost | null): Cost | null {
  if (!next) return prev
  if (prev?.costFrom !== 'calls') return next
  if (next.costFrom !== 'calls') return prev
  return summedMore(prev, next) ? prev : next
}

// summedMore: a holds more than b, by tokens, then by the known cost.
function summedMore(a: Cost, b: Cost): boolean {
  if (a.tokens !== b.tokens) return a.tokens > b.tokens
  return (Number(a.costUSD) || 0) > (Number(b.costUSD) || 0)
}
