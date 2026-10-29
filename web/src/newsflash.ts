import type { Block, ResearchItem } from './types'

export interface TopicGroup {
  // topic is as spec.topics spells it; '' for items without a topic.
  topic: string
  items: ResearchItem[]
}

// newsflash takes the items out of a result's blocks and groups them by
// topic: the research's topics in their order, then the other topics
// alphabetically, then items without one. Within a topic the important
// items come first, then the newest. rest are the other blocks, in order.
export function newsflash(blocks: Block[], topics: string[] = []): { groups: TopicGroup[]; rest: Block[] } {
  const key = (t: string | undefined) => (t ?? '').trim().toLowerCase()
  const groups = new Map<string, TopicGroup>()
  for (const t of topics) if (!groups.has(key(t))) groups.set(key(t), { topic: t, items: [] })
  const configured = groups.size
  const rest: Block[] = []
  for (const b of blocks) {
    if (b.type !== 'items') {
      rest.push(b)
      continue
    }
    for (const it of b.items) {
      const k = key(it.topic)
      let g = groups.get(k)
      if (!g) groups.set(k, (g = { topic: it.topic?.trim() ?? '', items: [] }))
      g.items.push(it)
    }
  }
  const all = [...groups.values()]
  const others = all
    .slice(configured)
    .filter((g) => g.topic !== '')
    .sort((a, b) => a.topic.localeCompare(b.topic))
  const ordered = [...all.slice(0, configured), ...others, ...all.filter((g) => g.topic === '')]
  for (const g of ordered) g.items.sort(byImportanceThenNewest)
  return { groups: ordered.filter((g) => g.items.length > 0), rest }
}

function byImportanceThenNewest(a: ResearchItem, b: ResearchItem): number {
  return b.importance - a.importance || published(b) - published(a)
}

// published is an item's time in ms; unknown sorts last (as 1970).
function published(it: ResearchItem): number {
  const t = it.publishedAt ? Date.parse(it.publishedAt) : NaN
  return Number.isNaN(t) ? 0 : t
}
