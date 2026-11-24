<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import Markdown from '../components/Markdown.vue'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { deleteResearch, getResearch } from '../client'
import { useNow, useResource } from '../live'
import { localMinute, nextRunTitle } from '../localTime'
import { clockTime, duration, errorMessage, researchRoute, safeURL } from '../format'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import Badge from '../components/Badge.vue'
import CronSchedule from '../components/CronSchedule.vue'
import LocalText from '../components/LocalText.vue'
import RelTime from '../components/RelTime.vue'
import { pageHeaderUi } from '../pageHeader'
import ResearchResult from '../components/ResearchResult.vue'
import NotFound from './NotFound.vue'
import { announceChange } from '../ui/useAnnounce'

// A research: its header (name, view, schedule, agent, feeds, actions), the
// run in progress and the last result. Utilities only (docs/contributing/web-ui.md, "Research
// page"): header rows stack on phones, where links and buttons are 44 px high.
const props = defineProps<{ namespace: string; name: string }>()
// The props are read outside the closure, so another research loads again.
const { data, error, notFound, loading } = useResource(() => {
  const { namespace, name } = props
  return () => getResearch(namespace, name)
}, ['research'])
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
const pendingLabel = { starting: 'Starting', waiting: 'Waiting', running: 'Running' } as const
// A change of the run is announced (useAnnounce.ts), not read from the card.
announceChange(() =>
  r.value
    ? { key: `${props.namespace}/${props.name}`, text: pending.value ? `Research now ${pendingLabel[pending.value]}` : 'Research run done' }
    : null,
)
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
    await deleteResearch(x.namespace, x.name)
    await router.push('/research')
  } catch (e) {
    deleteError.value = errorMessage(e)
  } finally {
    deleting.value = false
  }
}
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">

    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" :description="error" />
    <div v-if="loading && !r" class="card empty">Loading…</div>

    <template v-if="r && data">
      <header class="flex min-w-0 flex-col gap-2" data-research-head>
        <!-- Eyebrow: the kind and depth; the meta line: namespace,
             schedule, agent and runs; Run history and Delete on the right. -->
        <UPageHeader :title="r.name" :ui="pageHeaderUi">
          <template #headline>{{ r.view === 'newsflash' ? 'News flash' : 'Report' }} · {{ r.depth }}</template>
          <template #description>
            <span class="flex flex-col gap-1.5 tablet:flex-row tablet:flex-wrap tablet:gap-x-[18px]" data-research-facts>
              <span class="[overflow-wrap:anywhere]">{{ r.namespace }}</span>
              <CronSchedule v-if="r.schedule" :schedule="r.schedule" :time-zone="r.timeZone" />
              <span v-else>one-off<template v-if="r.createdBy"> by {{ r.createdBy }}</template></span>
              <span class="[overflow-wrap:anywhere]">agent <code>{{ agent }}</code></span>
              <span>last run <RelTime :time="r.lastRun" /></span>
              <span v-if="r.nextRun">next run <time :datetime="r.nextRun" :title="nextRunTitle(r.nextRun, now)">{{ localMinute(r.nextRun, now) }}</time></span>
            </span>
          </template>
          <template #links>
            <UButton
              :to="`${researchRoute(r.namespace, r.name)}/runs`"
              color="neutral"
              variant="outline"
              size="sm"
              class="min-h-11 justify-center tablet:min-h-0"
              label="Run history"
            />
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
          </template>
        </UPageHeader>
        <Markdown class="mt-1 max-w-[920px] text-(--text-2)" :class="{ 'line-clamp-3': !showPrompt }" :text="r.prompt" />
        <UButton
          v-if="r.prompt.length > 240"
          color="neutral"
          variant="link"
          size="sm"
          class="min-h-11 self-start p-0 text-[13px] text-(--link) tablet:min-h-0"
          :aria-expanded="showPrompt"
          :label="showPrompt ? 'Show less' : 'Show the whole prompt'"
          @click="showPrompt = !showPrompt"
        />
        <div v-if="r.sources?.length" class="flex flex-wrap items-center gap-x-2 text-[13px] text-(--text-2)" data-research-feeds>
          <span>Feeds:</span>
          <template v-for="(s, i) in r.sources" :key="s.url">
            <span v-if="i" aria-hidden="true">·</span>
            <UTooltip v-if="safeURL(s.url)" :text="s.topic" :disabled="!s.topic">
              <a
                :href="safeURL(s.url)"
                target="_blank"
                rel="noopener noreferrer"
                class="inline-flex min-h-11 items-center [overflow-wrap:anywhere] tablet:min-h-0"
              >{{ feedLabel(s) }}</a>
            </UTooltip>
            <span v-else class="[overflow-wrap:anywhere]" :title="s.topic || undefined">{{ feedLabel(s) }}</span>
          </template>
        </div>
        <UAlert v-if="deleteError" role="alert" :color="alertColor('danger')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" :description="deleteError" />
        <div v-if="!pending && (r.waiting || r.skippedReason)" class="flex flex-wrap items-center gap-2 text-[13px]">
          <Badge v-if="r.waiting" tone="warning">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </Badge>
          <span v-if="r.skippedReason" class="text-(--text-2) [overflow-wrap:anywhere]"><LocalText :text="r.skippedReason" /></span>
        </div>
        <UAlert v-if="r.run?.outcome === 'failed' && r.run.message" :color="alertColor('danger')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" title="The last run failed">
          <template #description><LocalText :text="r.run.message" /></template>
        </UAlert>
      </header>

      <div v-if="pending" class="card section flex flex-wrap items-center gap-x-3 gap-y-1.5 px-[18px] py-4">
        <template v-if="pending === 'waiting'">
          <Badge tone="warning" dot>Waiting</Badge>
          <span v-if="r.waiting">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </span>
          <span v-else>waiting for a usage window to reset</span>
          <span v-if="r.skippedReason || r.run?.reason" :class="note"><LocalText :text="r.skippedReason || r.run?.reason" /></span>
        </template>
        <template v-else-if="pending === 'running'">
          <Badge tone="progress" dot pulse>Running</Badge>
          <span v-if="runAgent" class="[overflow-wrap:anywhere]"><code>{{ runAgent }}</code></span>
          <span v-if="elapsed" class="text-(--text-2)">for {{ elapsed }}</span>
          <span v-if="r.run?.reason" :class="note"><LocalText :text="r.run.reason" /></span>
        </template>
        <template v-else>
          <Badge tone="muted" dot pulse>Starting</Badge>
          <span class="text-(--text-2)">The operator is choosing an agent for the run.</span>
        </template>
        <span class="basis-full text-[13px] text-(--text-3)">The result appears when the run is done, without a reload.</span>
      </div>

      <ResearchResult v-if="data.result" :research="r" :run="data.run" :result="data.result" run-link class="section" />
      <UEmpty v-else-if="!pending" icon="i-lucide-file-text" title="No result yet." class="section" />
    </template>
  </div>
</template>
