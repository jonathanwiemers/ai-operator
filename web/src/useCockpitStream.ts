import { computed, ref, watch } from 'vue'
import { hasFeature } from './api'
import { getStream, LoginRequiredError } from './client'
import { errorMessage } from './format'
import { live } from './live'
import { cockpitItems, cockpitStreamQuery, streamEventKey } from './stream'
import type { StreamEvent } from './types'

// CockpitStreamScope is the part of the stream a Cockpit shows: one
// milestone, a project's milestones, or everything (no namespace).
export interface CockpitStreamScope {
  namespace?: string
  name?: string
  milestoneIID?: number
}

// useCockpitStream loads a Cockpit's stream (R10 of "Desk: the Cockpit"):
// the newest page, older pages on request, newer events on each live
// change, and a search. items are the events sorted by cockpitItems,
// oldest first. The milestone Cockpit uses it now, the project and
// Overview Cockpits later (D785-P2).
export function useCockpitStream(scope: () => CockpitStreamScope) {
  const events = ref<StreamEvent[]>([])
  const older = ref<string | undefined>()
  const newest = ref<string | undefined>()
  const historyOn = ref(true)
  const loading = ref(true)
  const loadingOlder = ref(false)
  const error = ref('')
  const q = ref('')
  // seq drops the answers of a load that a newer one (another scope or
  // search) replaced.
  let seq = 0

  const query = (extra: { before?: string; after?: string } = {}) =>
    cockpitStreamQuery(scope(), { chat: hasFeature('chat'), q: q.value, ...extra })
  const failed = (e: unknown) => {
    if (!(e instanceof LoginRequiredError)) error.value = errorMessage(e)
  }

  async function reload() {
    const mine = ++seq
    loading.value = true
    error.value = ''
    try {
      const page = await getStream(query())
      if (mine !== seq) return
      events.value = page.events
      older.value = page.older
      newest.value = page.newest
      historyOn.value = page.history
    } catch (e) {
      if (mine === seq) failed(e)
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  async function loadOlder() {
    if (!older.value || loadingOlder.value) return
    const mine = seq
    loadingOlder.value = true
    try {
      const page = await getStream(query({ before: older.value }))
      if (mine !== seq) return
      events.value = [...events.value, ...page.events]
      older.value = page.older
    } catch (e) {
      if (mine === seq) failed(e)
    } finally {
      loadingOlder.value = false
    }
  }

  // loadLive adds what is newer than the newest event; an empty stream (no
  // events yet, or a search without hits) has no cursor, so it asks for
  // the newest page again, without the Loading state.
  async function loadLive() {
    if (loading.value) return
    const mine = seq
    try {
      if (!newest.value) {
        const page = await getStream(query())
        if (mine !== seq || newest.value) return
        events.value = page.events
        older.value = page.older
        newest.value = page.newest
        return
      }
      const page = await getStream(query({ after: newest.value }))
      if (mine !== seq || !page.events.length) return
      const seen = new Set(events.value.map(streamEventKey))
      const fresh = page.events.filter((e) => !seen.has(streamEventKey(e)))
      if (fresh.length) events.value = [...fresh, ...events.value]
      if (page.newest) newest.value = page.newest
    } catch {
      /* live refresh is best-effort */
    }
  }

  // search starts over from the newest event with the text; '' clears it.
  function search(text: string) {
    q.value = text
    void reload()
  }

  const kind = (s: CockpitStreamScope) => (s.milestoneIID ? 'milestone' : s.namespace ? 'project' : 'all')
  const items = computed(() => cockpitItems(events.value, { scope: kind(scope()) }))

  watch(
    () => {
      const s = scope()
      return `${s.namespace ?? ''}/${s.name ?? ''}/${s.milestoneIID ?? ''}`
    },
    () => {
      events.value = []
      void reload()
    },
    { immediate: true },
  )
  watch(
    () => live.version,
    () => {
      if (!live.kinds || live.kinds.some((k) => k === 'project' || k === 'task')) void loadLive()
    },
  )

  return { items, older, historyOn, loading, loadingOlder, error, q, search, reload, loadOlder }
}
