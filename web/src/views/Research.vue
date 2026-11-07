<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { del } from '../api'
import { useNow, useResource } from '../live'
import { clockTime, duration, safeURL } from '../format'
import Badge from '../components/Badge.vue'
import RelTime from '../components/RelTime.vue'
import ResearchResult from '../components/ResearchResult.vue'
import NotFound from './NotFound.vue'
import type { ResearchResponse } from '../types'

const props = defineProps<{ namespace: string; name: string }>()
const { data, error, notFound, loading } = useResource<ResearchResponse>(
  () => `/researches/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}`,
  ['research'],
)
const router = useRouter()
const now = useNow()
const r = computed(() => data.value?.research)
const agent = computed(() => {
  const a = r.value?.agent
  if (!a) return 'auto'
  return `${a.engine}/${a.model}${a.effort ? ` (${a.effort})` : ''}`
})
const showPrompt = ref(false)

const feedHost = (u: string) => {
  const s = safeURL(u)
  return s ? new URL(s).host : u
}

const feedLabel = (s: { url: string; name?: string }) => s.name || feedHost(s.url)

// pending is the run the page follows live: a one-off that has not started
// yet, waits for a usage reset, or any run in progress. The research events
// refetch the page, so the report replaces it once the run is done.
const pending = computed<'starting' | 'waiting' | 'running' | null>(() => {
  const x = r.value
  if (!x) return null
  if (x.phase === 'Running') return 'running'
  if (x.oneOff && (x.phase === 'Waiting' || x.waiting)) return 'waiting'
  if (x.oneOff && !x.lastRun && (!x.phase || x.phase === 'Scheduled')) return 'starting'
  return null
})
const runAgent = computed(() => {
  const run = r.value?.run
  if (!run?.engine) return ''
  return `${run.engine}${run.model ? `/${run.model}` : ''}${run.effort ? ` (${run.effort})` : ''}`
})
const elapsed = computed(() => {
  const start = Date.parse(r.value?.run?.startedAt ?? '')
  return Number.isNaN(start) ? '' : duration(Math.max(0, now.value - start))
})

const deleting = ref(false)
const deleteError = ref<string | null>(null)

async function remove() {
  const x = r.value
  if (!x || !window.confirm(`Delete the research ${x.name}? Its stored results stay until they expire.`)) return
  deleting.value = true
  deleteError.value = null
  try {
    await del(`/researches/${encodeURIComponent(x.namespace)}/${encodeURIComponent(x.name)}`)
    await router.push('/research')
  } catch (e) {
    deleteError.value = e instanceof Error ? e.message : String(e)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !r" class="card empty">Loading…</div>

    <template v-if="r && data">
      <header class="head">
        <div class="row">
          <h1>{{ r.name }}</h1>
          <span class="ns faint">{{ r.namespace }}</span>
          <span class="spacer" />
          <Badge>{{ r.view === 'newsflash' ? 'News flash' : 'Report' }}</Badge>
          <Badge tone="muted">{{ r.depth }}</Badge>
          <button v-if="r.canDelete" type="button" class="btn danger" :disabled="deleting" @click="remove">
            {{ deleting ? 'Deleting…' : 'Delete' }}
          </button>
        </div>
        <p class="prompt prose" :class="{ clamped: !showPrompt }">{{ r.prompt }}</p>
        <button v-if="r.prompt.length > 240" type="button" class="more" @click="showPrompt = !showPrompt">
          {{ showPrompt ? 'Show less' : 'Show the whole prompt' }}
        </button>
        <div class="facts-line muted">
          <span v-if="r.schedule"><code>{{ r.schedule }}</code> {{ r.timeZone || 'UTC' }}</span>
          <span v-else>one-off<template v-if="r.createdBy"> by {{ r.createdBy }}</template></span>
          <span>agent <code>{{ agent }}</code></span>
          <span>last run <RelTime :time="r.lastRun" /></span>
          <span v-if="r.nextRun">next run <RelTime :time="r.nextRun" /></span>
          <RouterLink
            :to="`/research/${encodeURIComponent(r.namespace)}/${encodeURIComponent(r.name)}/runs`"
            class="history"
          >
            Run history
          </RouterLink>
        </div>
        <div v-if="r.sources?.length" class="feeds muted">
          Feeds:
          <template v-for="(s, i) in r.sources" :key="s.url">
            <template v-if="i"> · </template>
            <a
              v-if="safeURL(s.url)"
              :href="safeURL(s.url)"
              target="_blank"
              rel="noopener noreferrer"
              :title="s.topic || undefined"
            >{{ feedLabel(s) }}</a>
            <span v-else :title="s.topic || undefined">{{ feedLabel(s) }}</span>
          </template>
        </div>
        <div v-if="deleteError" class="banner danger">{{ deleteError }}</div>
        <div v-if="!pending && (r.waiting || r.skippedReason)" class="waiting">
          <Badge v-if="r.waiting" tone="warning">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </Badge>
          <span v-if="r.skippedReason" class="muted">{{ r.skippedReason }}</span>
        </div>
        <div v-if="r.run?.outcome === 'failed' && r.run.message" class="banner danger">
          <strong>The last run failed</strong>
          <span>{{ r.run.message }}</span>
        </div>
      </header>

      <div v-if="pending" class="card live section" aria-live="polite">
        <template v-if="pending === 'waiting'">
          <Badge tone="warning" dot>Waiting</Badge>
          <span v-if="r.waiting">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </span>
          <span v-else>waiting for a usage window to reset</span>
          <span v-if="r.skippedReason || r.run?.reason" class="muted reason">{{ r.skippedReason || r.run?.reason }}</span>
        </template>
        <template v-else-if="pending === 'running'">
          <Badge tone="progress" dot pulse>Running</Badge>
          <span v-if="runAgent"><code>{{ runAgent }}</code></span>
          <span v-if="elapsed" class="muted">for {{ elapsed }}</span>
          <span v-if="r.run?.reason" class="muted reason">{{ r.run.reason }}</span>
        </template>
        <template v-else>
          <Badge tone="muted" dot pulse>Starting</Badge>
          <span class="muted">The operator is choosing an agent for the run.</span>
        </template>
        <span class="faint hint">The result appears when the run is done, without a reload.</span>
      </div>

      <ResearchResult v-if="data.result" :research="r" :run="data.run" :result="data.result" run-link class="section" />
      <div v-else-if="!pending" class="card empty section">No result yet.</div>
    </template>
  </div>
</template>

<style scoped>
.head {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.ns {
  font-size: 12px;
}

.prompt {
  margin: 4px 0 0;
  max-width: 920px;
  color: var(--text-2);
}

.prompt.clamped {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  overflow: hidden;
}

.more {
  align-self: flex-start;
  padding: 0;
  border: none;
  background: none;
  color: var(--link);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.facts-line {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 18px;
  font-size: 13px;
}

.feeds {
  font-size: 13px;
}

.waiting {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.history {
  font-weight: 550;
}

.live {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 12px;
  padding: 16px 18px;
}

.live .reason,
.live .hint {
  flex-basis: 100%;
  font-size: 13px;
}

.btn.danger {
  height: 28px;
  color: var(--tone-danger-fg);
}

.btn:disabled {
  opacity: 0.55;
  cursor: default;
}
</style>
