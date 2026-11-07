// The tabs of a view. The selected tab lives in the ?tab= query parameter
// so it can be linked to and reloaded; the first tab of a list is the
// default and is left out.
//
// Project and milestone streams: Stream | Board | Spend (decision 7).
// The dashboard at /: Overview | Stream (decision 3).
export type StreamTab = 'stream' | 'board' | 'spend'
export type DashboardTab = 'overview' | 'stream'

export interface Tab<T extends string> {
  id: T
  label: string
}

export const streamTabs: Tab<StreamTab>[] = [
  { id: 'stream', label: 'Stream' },
  { id: 'board', label: 'Board' },
  { id: 'spend', label: 'Spend' },
]

export const dashboardTabs: Tab<DashboardTab>[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'stream', label: 'Stream' },
]

type QueryValue = string | null | (string | null)[] | undefined

// tabFromQuery reads ?tab= against tabs (the stream tabs by default):
// unknown, empty or repeated values fall back to the first known one, else
// to the list's first tab.
export function tabFromQuery(v: QueryValue): StreamTab
export function tabFromQuery<T extends string>(v: QueryValue, tabs: readonly Tab<T>[]): T
export function tabFromQuery(v: QueryValue, tabs: readonly Tab<string>[] = streamTabs): string {
  const list = Array.isArray(v) ? v : [v]
  for (const s of list) {
    const t = tabs.find((x) => x.id === s)
    if (t) return t.id
  }
  return tabs[0]!.id
}

// tabQuery is the query for a tab, keeping the other parameters; the
// list's first tab (stream by default) drops the parameter.
export function tabQuery<Q extends Record<string, unknown>>(
  query: Q,
  tab: string,
  tabs: readonly Tab<string>[] = streamTabs,
): Omit<Q, 'tab'> & { tab?: string } {
  const { tab: _, ...rest } = query
  return tab === tabs[0]!.id ? rest : { ...rest, tab }
}
