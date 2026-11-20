// The Desk's one announcer (Decision 7 of docs/specs/desk-accessible.spec.md):
// components call announce() instead of being live regions themselves.
// App.vue renders the two visually hidden live regions once, from
// announcer.polite and announcer.assertive; the queue (announce.ts)
// throttles and dedupes. Toasts have their own live region:
// do not announce what a toast says.
import { nextTick, reactive, readonly, watch } from 'vue'
import { createAnnouncer, joinAnnouncements, type Politeness } from '../announce'

const regions = reactive({ polite: '', assertive: '' })

// The texts each region gets on the next tick.
const next: Record<Politeness, string[]> = { polite: [], assertive: [] }

// A region is cleared first and set on the next tick, so a screen reader
// reads a text again that was there before. Texts that come in the same
// tick (two errors at once) are said together, so none overwrites another.
function show(text: string, politeness: Politeness) {
  const texts = next[politeness]
  texts.push(text)
  if (texts.length > 1) return
  regions[politeness] = ''
  void nextTick(() => {
    regions[politeness] = joinAnnouncements(texts.splice(0))
  })
}

const queue = createAnnouncer({
  now: () => Date.now(),
  schedule: (fn, ms) => window.setTimeout(fn, ms),
  emit: show,
})

// announcer is what the live regions show.
export const announcer = readonly(regions)

// announce says text politely, or with assertive (errors only) at once.
export function announce(text: string, opts: { assertive?: boolean } = {}): void {
  queue.say(text, opts.assertive ? 'assertive' : 'polite')
}

// announceChange says the open item's text when it changes ("Task now
// Waiting for you"): not on the first load, and not when another item
// (another key) opens. A null item (still loading) changes nothing.
export function announceChange(item: () => { key: string; text: string } | null): void {
  let last: { key: string; text: string } | null = null
  watch(
    item,
    (next) => {
      if (!next) return
      if (last && last.key === next.key && last.text !== next.text) announce(next.text)
      last = next
    },
    { immediate: true },
  )
}

export function useAnnounce() {
  return { announcer, announce }
}
