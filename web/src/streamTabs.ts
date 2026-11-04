// The tabs of a project or milestone stream (decision 7: Stream | Board,
// Spend follows). The selected tab lives in the ?tab= query parameter so it
// can be linked to and reloaded; stream is the default and is left out.
export type StreamTab = 'stream' | 'board'

export const streamTabs: { id: StreamTab; label: string }[] = [
  { id: 'stream', label: 'Stream' },
  { id: 'board', label: 'Board' },
]

type QueryValue = string | null | (string | null)[] | undefined

// tabFromQuery reads ?tab=: unknown, empty or repeated values fall back to
// the first known one, else to stream.
export function tabFromQuery(v: QueryValue): StreamTab {
  const list = Array.isArray(v) ? v : [v]
  for (const s of list) {
    const t = streamTabs.find((x) => x.id === s)
    if (t) return t.id
  }
  return 'stream'
}

// tabQuery is the query for a tab, keeping the other parameters.
export function tabQuery<Q extends Record<string, unknown>>(query: Q, tab: StreamTab): Omit<Q, 'tab'> & { tab?: string } {
  const { tab: _, ...rest } = query
  return tab === 'stream' ? rest : { ...rest, tab }
}
