import { onMounted, onUnmounted, reactive, ref, watch, type Ref } from 'vue'
import { get, session, LoginRequiredError, NotFoundError } from './api'

// live tracks the server's event stream. Every burst of change events bumps
// version; views reload what they show when it changes. kinds are the kinds
// of the burst's events (project, task, research), null when changes may
// have been missed.
export const live = reactive({
  connected: false,
  version: 0,
  kinds: null as string[] | null,
})

let source: EventSource | null = null
let bumpTimer: number | undefined
let pendingKinds: Set<string> | null = new Set()
let retryTimer: number | undefined
let retryDelay = 2000

export function connectEvents(): void {
  if (source) return
  window.clearTimeout(retryTimer)
  source = new EventSource('/api/v1/events')
  source.addEventListener('open', () => {
    // Changes may have been missed while the stream was down.
    if (!live.connected) bump(null)
    live.connected = true
    retryDelay = 2000
  })
  source.addEventListener('change', (e) => bump(eventKind(e)))
  source.addEventListener('error', () => {
    live.connected = false
    // EventSource retries network errors by itself, but gives up on any
    // non-200 answer (an expired login, a restarting pod behind the
    // Gateway). Only a 401 ends it for good; anything else is retried.
    if (source?.readyState !== EventSource.CLOSED) return
    source = null
    get('/me').then(scheduleRetry, (e) => {
      if (!(e instanceof LoginRequiredError)) scheduleRetry()
    })
  })
}

function scheduleRetry(): void {
  window.clearTimeout(retryTimer)
  retryTimer = window.setTimeout(connectEvents, retryDelay)
  retryDelay = Math.min(retryDelay * 2, 60000)
}

export function disconnectEvents(): void {
  window.clearTimeout(retryTimer)
  source?.close()
  source = null
  live.connected = false
}

function eventKind(e: MessageEvent): string | null {
  try {
    return (JSON.parse(e.data) as { kind?: string }).kind ?? null
  } catch {
    return null
  }
}

// bump coalesces bursts of events (a run writes its status step by step);
// kind null stands for every kind.
function bump(kind: string | null): void {
  if (kind === null) pendingKinds = null
  else pendingKinds?.add(kind)
  window.clearTimeout(bumpTimer)
  bumpTimer = window.setTimeout(() => {
    live.kinds = pendingKinds && [...pendingKinds]
    pendingKinds = new Set()
    live.version++
  }, 400)
}

// useResource loads path now, when it changes, and on every change event,
// or with kinds only on events of those kinds; an empty path leaves data
// null.
export function useResource<T>(path: () => string, kinds?: string[]) {
  const data = ref<T | null>(null) as Ref<T | null>
  const error = ref<string | null>(null)
  const notFound = ref(false)
  const loading = ref(true)
  let seq = 0

  async function load() {
    const mine = ++seq
    const p = path()
    // An empty path loads nothing (a resource that does not exist yet).
    if (!p) {
      data.value = null
      error.value = null
      notFound.value = false
      loading.value = false
      return
    }
    try {
      const v = await get<T>(p)
      if (mine !== seq) return
      data.value = v
      error.value = null
      notFound.value = false
    } catch (e) {
      if (mine !== seq || e instanceof LoginRequiredError) return
      if (e instanceof NotFoundError) {
        notFound.value = true
        data.value = null
      } else {
        error.value = e instanceof Error ? e.message : String(e)
      }
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  watch(path, () => {
    loading.value = true
    data.value = null
    void load()
  })
  watch(
    () => live.version,
    () => {
      if (session.loginRequired) return
      if (!kinds || !live.kinds || live.kinds.some((k) => kinds.includes(k))) void load()
    },
  )
  onMounted(load)
  return { data, error, notFound, loading, reload: load }
}

// now is the current time, updated every 15 seconds for relative times.
export const now = ref(Date.now())
let nowUsers = 0
let nowTimer: number | undefined

export function useNow(): Ref<number> {
  onMounted(() => {
    if (nowUsers++ === 0) {
      now.value = Date.now()
      nowTimer = window.setInterval(() => (now.value = Date.now()), 15000)
    }
  })
  onUnmounted(() => {
    if (--nowUsers === 0) window.clearInterval(nowTimer)
  })
  return now
}
