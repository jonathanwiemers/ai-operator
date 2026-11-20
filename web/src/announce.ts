// The announcer's queue (Decision 7 of docs/specs/desk-accessible.spec.md),
// pure and clock-injected so node can test it; src/ui/useAnnounce.ts puts
// what it emits into the two live regions in App.vue.
//
// - Polite messages go out at most once per gapMs. Those that come faster
//   wait in order, the newest queueMax only (D640-P2).
// - Assertive messages (errors) go out at once.
// - The same text in the same politeness within dedupeMs is dropped.
// - Empty text is ignored.

export type Politeness = 'polite' | 'assertive'

export interface AnnouncerOptions {
  now: () => number
  schedule: (fn: () => void, ms: number) => void
  emit: (text: string, politeness: Politeness) => void
  gapMs?: number
  dedupeMs?: number
  queueMax?: number
}

export interface Announcer {
  say(text: string, politeness?: Politeness): void
}

export function createAnnouncer(o: AnnouncerOptions): Announcer {
  const gap = o.gapMs ?? 2000
  const dedupe = o.dedupeMs ?? 10000
  const max = o.queueMax ?? 3
  // When each "<politeness>:<text>" was last accepted.
  const said = new Map<string, number>()
  const queue: string[] = []
  let lastPolite = -Infinity
  let scheduled = false

  function flush() {
    scheduled = false
    const text = queue.shift()
    if (text === undefined) return
    lastPolite = o.now()
    o.emit(text, 'polite')
    if (queue.length) wait()
  }

  function wait() {
    if (scheduled) return
    scheduled = true
    o.schedule(flush, Math.max(0, lastPolite + gap - o.now()))
  }

  return {
    say(raw, politeness = 'polite') {
      const text = raw.replace(/\s+/g, ' ').trim()
      if (!text) return
      const t = o.now()
      for (const [k, at] of said) if (t - at >= dedupe) said.delete(k)
      const key = `${politeness}:${text}`
      if (said.has(key)) return
      said.set(key, t)
      if (politeness === 'assertive') {
        o.emit(text, 'assertive')
        return
      }
      if (!queue.length && !scheduled && t - lastPolite >= gap) {
        lastPolite = t
        o.emit(text, 'polite')
        return
      }
      queue.push(text)
      // A dropped message was never said: it may come again.
      while (queue.length > max) said.delete(`polite:${queue.shift()}`)
      wait()
    },
  }
}

// inboxAnnouncement is what a change of the Inbox count says.
export function inboxAnnouncement(count: number): string {
  if (count <= 0) return 'Inbox empty'
  return `Inbox: ${count} ${count === 1 ? 'item' : 'items'}`
}

// joinAnnouncements says several texts as one, each ending as a sentence.
export function joinAnnouncements(texts: string[]): string {
  if (texts.length === 1) return texts[0]!
  return texts.map((t) => (/[.!?…]$/.test(t) ? t : `${t}.`)).join(' ')
}
