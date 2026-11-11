<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import Markdown from '../components/Markdown.vue'
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

// A research: its header (name, view, schedule, agent, feeds, actions), the
// run in progress and the last result. Utilities only (docs/ui.md, "Research
// page"): header rows stack on phones, where links and buttons are 44 px high.
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
// note: a reason under the live line, on a line of its own.
const note = 'basis-full text-[13px] text-(--text-2) [overflow-wrap:anywhere]'

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

    <div v-if="error" class="banner danger [overflow-wrap:anywhere]">{{ error }}</div>
    <div v-if="loading && !r" class="card empty">Loading…</div>

    <template v-if="r && data">
      <header class="flex min-w-0 flex-col gap-2" data-research-head>
        <div class="flex flex-col gap-2 tablet:flex-row tablet:flex-wrap tablet:items-center">
          <div class="flex min-w-0 flex-wrap items-baseline gap-x-2">
            <h1 class="[overflow-wrap:anywhere]">{{ r.name }}</h1>
            <span class="text-xs text-(--text-3)">{{ r.namespace }}</span>
          </div>
          <div class="flex flex-wrap items-center gap-2 tablet:ms-auto">
            <Badge>{{ r.view === 'newsflash' ? 'News flash' : 'Report' }}</Badge>
            <Badge tone="muted">{{ r.depth }}</Badge>
          </div>
          <UButton
            v-if="r.canDelete"
            color="neutral"
            variant="outline"
            size="sm"
            class="min-h-11 justify-center text-(--tone-danger-fg) tablet:min-h-0"
            :disabled="deleting"
            :label="deleting ? 'Deleting…' : 'Delete'"
            @click="remove"
          />
        </div>
        <Markdown class="mt-1 max-w-[920px] text-(--text-2)" :class="{ 'line-clamp-3': !showPrompt }" :text="r.prompt" />
        <button
          v-if="r.prompt.length > 240"
          type="button"
          class="min-h-11 cursor-pointer self-start border-0 bg-transparent p-0 text-[13px] text-(--link) tablet:min-h-0"
          :aria-expanded="showPrompt"
          @click="showPrompt = !showPrompt"
        >
          {{ showPrompt ? 'Show less' : 'Show the whole prompt' }}
        </button>
        <div class="flex flex-col gap-1.5 text-[13px] text-(--text-2) tablet:flex-row tablet:flex-wrap tablet:gap-x-[18px]" data-research-facts>
          <span v-if="r.schedule" class="[overflow-wrap:anywhere]"><code>{{ r.schedule }}</code> {{ r.timeZone || 'UTC' }}</span>
          <span v-else>one-off<template v-if="r.createdBy"> by {{ r.createdBy }}</template></span>
          <span class="[overflow-wrap:anywhere]">agent <code>{{ agent }}</code></span>
          <span>last run <RelTime :time="r.lastRun" /></span>
          <span v-if="r.nextRun">next run <RelTime :time="r.nextRun" /></span>
          <RouterLink
            :to="`/research/${encodeURIComponent(r.namespace)}/${encodeURIComponent(r.name)}/runs`"
            class="inline-flex min-h-11 items-center self-start font-[550] tablet:min-h-0"
          >
            Run history
          </RouterLink>
        </div>
        <div v-if="r.sources?.length" class="flex flex-wrap items-center gap-x-2 text-[13px] text-(--text-2)" data-research-feeds>
          <span>Feeds:</span>
          <template v-for="(s, i) in r.sources" :key="s.url">
            <span v-if="i" aria-hidden="true">·</span>
            <a
              v-if="safeURL(s.url)"
              :href="safeURL(s.url)"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex min-h-11 items-center [overflow-wrap:anywhere] tablet:min-h-0"
              :title="s.topic || undefined"
            >{{ feedLabel(s) }}</a>
            <span v-else class="[overflow-wrap:anywhere]" :title="s.topic || undefined">{{ feedLabel(s) }}</span>
          </template>
        </div>
        <div v-if="deleteError" class="banner danger [overflow-wrap:anywhere]" role="alert">{{ deleteError }}</div>
        <div v-if="!pending && (r.waiting || r.skippedReason)" class="flex flex-wrap items-center gap-2 text-[13px]">
          <Badge v-if="r.waiting" tone="warning">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </Badge>
          <span v-if="r.skippedReason" class="text-(--text-2) [overflow-wrap:anywhere]">{{ r.skippedReason }}</span>
        </div>
        <div v-if="r.run?.outcome === 'failed' && r.run.message" class="banner danger [overflow-wrap:anywhere]">
          <strong>The last run failed</strong>
          <span>{{ r.run.message }}</span>
        </div>
      </header>

      <div v-if="pending" class="card section flex flex-wrap items-center gap-x-3 gap-y-1.5 px-[18px] py-4" aria-live="polite">
        <template v-if="pending === 'waiting'">
          <Badge tone="warning" dot>Waiting</Badge>
          <span v-if="r.waiting">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </span>
          <span v-else>waiting for a usage window to reset</span>
          <span v-if="r.skippedReason || r.run?.reason" :class="note">{{ r.skippedReason || r.run?.reason }}</span>
        </template>
        <template v-else-if="pending === 'running'">
          <Badge tone="progress" dot pulse>Running</Badge>
          <span v-if="runAgent" class="[overflow-wrap:anywhere]"><code>{{ runAgent }}</code></span>
          <span v-if="elapsed" class="text-(--text-2)">for {{ elapsed }}</span>
          <span v-if="r.run?.reason" :class="note">{{ r.run.reason }}</span>
        </template>
        <template v-else>
          <Badge tone="muted" dot pulse>Starting</Badge>
          <span class="text-(--text-2)">The operator is choosing an agent for the run.</span>
        </template>
        <span class="basis-full text-[13px] text-(--text-3)">The result appears when the run is done, without a reload.</span>
      </div>

      <ResearchResult v-if="data.result" :research="r" :run="data.run" :result="data.result" run-link class="section" />
      <div v-else-if="!pending" class="card empty section">No result yet.</div>
    </template>
  </div>
</template>
