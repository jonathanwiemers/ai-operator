<script setup lang="ts">
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed, ref, watch, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import UButton from '@nuxt/ui/components/Button.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import USelect from '@nuxt/ui/components/Select.vue'
import Badge from '../components/Badge.vue'
import { hasFeature } from '../api'
import { getInbox, getMilestone, getProject, getProjectHistory, getStream, LoginRequiredError, NotFoundError } from '../client'
import { setCrumbNames } from '../crumbNames'
import Board from '../components/Board.vue'
import ContextLayout from '../components/ContextLayout.vue'
import ContextPanel from '../components/ContextPanel.vue'
import CostChip from '../components/CostChip.vue'
import PauseCardCompact from '../components/PauseCardCompact.vue'
import ProjectBoards from '../components/ProjectBoards.vue'
import SpendPanel from '../components/SpendPanel.vue'
import Composer from '../components/Composer.vue'
import EventCard from '../components/stream/EventCard.vue'
import MilestoneDecisions from '../components/MilestoneDecisions.vue'
import MilestoneIssues from '../components/MilestoneIssues.vue'
import MilestoneNeeds from '../components/MilestoneNeeds.vue'
import MilestonePilot from '../components/MilestonePilot.vue'
import MilestoneRetro from '../components/MilestoneRetro.vue'
import MilestoneSections from '../components/MilestoneSections.vue'
import PlanList from '../components/PlanList.vue'
import ProjectFinishing from '../components/ProjectFinishing.vue'
import ProjectQueue from '../components/ProjectQueue.vue'
import RouteTabs from '../ui/RouteTabs.vue'
import { live, useResource } from '../live'
import { needsLabel, needsOpen } from '../needsOpen'
import { pausePrompt } from '../pauseCard'
import {
  dayEndCursor,
  dayLabel,
  groupThreads,
  heatLevel,
  kindsForFilter,
  runtimeKindsForFilter,
  runtimeStreamFilters,
  sameLocalDay,
  slashSuggestions,
  streamEventKey,
  streamFilters,
  type StreamFilter,
} from '../stream'
import type {
  Cost,
  HistoryResponse,
  MilestoneView,
  ProjectDetail,
  StreamEvent,
} from '../types'
import { milestoneStatus } from '../milestoneRows'
import { toneText } from '../badgeTone'
import { pageHeaderUi } from '../pageHeader'
import { useStreamNote } from '../streamNote'
import { decisionsQuery, milestoneFromQuery } from '../decisions'
import { milestoneTabFromRoute, milestoneTabs, projectTabsFor, storedMilestoneTabs, tabFromQuery, tabQuery } from '../streamTabs'
import NotFound from './NotFound.vue'

const props = defineProps<{
  namespace?: string
  name?: string
  iid?: string
}>()

const everything = computed(() => !props.namespace || !props.name)
const milestoneIID = computed(() => (props.iid ? Number(props.iid) : undefined))

// tab: Stream, Decisions, Board, Spend or Plans (feature plans) on project streams, Issues (the default),
// Needs you, Decisions, Pilot, Retrospective, Stream, Board or Spend on milestones, kept in ?tab= so it can be
// linked to (a #prompt-Q<n> link without it opens Needs you); Everything has no
// tabs. A stored milestone has no tasks, so no Board. The Needs you tab's
// label carries the count of its Open items ("Needs you · n", D530-P1).
const route = useRoute()
const tabs = computed(() => {
  if (everything.value) return []
  if (!milestoneIID.value) return projectTabsFor(hasFeature('plans'))
  const m = milestone.value?.iid === milestoneIID.value ? milestone.value : null
  const list = m?.stored ? storedMilestoneTabs : milestoneTabs
  const n = m ? needsOpen(m).count : 0
  return n ? list.map((t) => (t.id === 'needs' ? { ...t, label: needsLabel(n) } : t)) : list
})
const tab = computed((): string => {
  if (everything.value) return 'stream'
  if (milestoneIID.value) return milestoneTabFromRoute(route.query.tab, route.hash, tabs.value)
  return tabFromQuery(route.query.tab, tabs.value)
})
const filter = ref<StreamFilter>('all')

// The project's Decisions tab (R14): all milestones, or the one ?milestone=
// names, chosen from the project's milestones (one the list lacks shows as
// %<iid>).
const router = useRouter()
const decisionsMilestone = computed(() => milestoneFromQuery(route.query.milestone))
const decisionsMilestones = computed(() => project.value?.milestones ?? [])
const decisionsMilestoneItems = computed(() => {
  const items = [{ value: 0, label: 'All milestones' }, ...decisionsMilestones.value.map((m) => ({ value: m.iid, label: m.title || `%${m.iid}` }))]
  const iid = decisionsMilestone.value
  return iid && !items.some((i) => i.value === iid) ? [...items, { value: iid, label: `%${iid}` }] : items
})
function setDecisionsMilestone(iid: number) {
  void router.replace({ query: decisionsQuery(route.query, { milestone: iid }) })
}
// A milestone's Stream tab has the runtime kinds only; the Pilot tab the rest.
const filters = computed(() => (milestoneIID.value ? runtimeStreamFilters : streamFilters))
const q = ref('')
const qDraft = ref('')
const beforeJump = ref<string | undefined>()
const events = ref<StreamEvent[]>([])
const older = ref<string | undefined>()
const newest = ref<string | undefined>()
const historyOn = ref(true)
const loading = ref(true)
const loadingOlder = ref(false)
const error = ref('')
const notFound = ref(false)
let seq = 0

const project = ref<ProjectDetail | null>(null)

async function loadProject() {
  if (everything.value) {
    project.value = null
    return
  }
  try {
    project.value = await getProject(props.namespace!, props.name!)
  } catch (e) {
    if (e instanceof NotFoundError) {
      notFound.value = true
      project.value = null
    } else if (!(e instanceof LoginRequiredError)) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }
}

const { data: inbox } = useResource(() => getInbox, ['project', 'task'])

const history = ref<HistoryResponse | null>(null)
const milestone = ref<MilestoneView | null>(null)
// milestoneFailed: the scoped milestone could not be loaded (the Issues tab
// says so instead of loading forever).
const milestoneFailed = ref(false)

// The project's note composer (a milestone's is on its Pilot tab).
const note = useStreamNote(
  () => ({
    namespace: props.namespace ?? '',
    name: props.name ?? '',
    project: project.value,
    steer: milestone.value?.permissions?.steer,
    shown: tab.value === 'stream' && !everything.value && !milestoneIID.value,
  }),
  () => void loadInitial(),
)

const projectInbox = computed(() => {
  const items = inbox.value?.items ?? []
  if (everything.value) return items
  return items.filter((i) => i.namespace === props.namespace && i.project === props.name)
})

// The context panel (docs/contributing/web-ui.md, "Context panel and pause history"), laid out by
// ContextLayout: beside a project stream from 1180 px; below, on project
// and milestone pages, the Context button opens it as a sheet. The
// button's count is projectInbox's, so the sheet loads nothing of its own.
const hasContext = computed(() => !everything.value && !!props.namespace && !!props.name)
const contextOpen = ref(false)

const canSteer = computed(() => !!milestone.value?.permissions?.steer)

// paused: the loaded milestone's paused autopilot, pinned above the composer
// (project and milestone streams only; the Inbox covers Everything).
const paused = computed(() => {
  const m = milestone.value
  return !everything.value && m?.autopilot?.phase === 'Paused' ? m : null
})

// pauseProps: the compact pause card's props (stream and Issues tabs).
const pauseProps = computed(() => {
  const m = paused.value
  if (!m || !props.namespace || !props.name) return null
  return {
    kind: m.autopilot?.pauseKind,
    reason: m.autopilot?.pausedReason,
    issue: m.autopilot?.pausedFor,
    milestoneTitle: m.title,
    waiting: m.autopilot?.waitingFor,
    exits: m.autopilot?.exits,
    prompt: pausePrompt(m.autopilot?.pauseKind, m.prompts),
    namespace: props.namespace,
    projectName: props.name,
    to: { name: 'stream-milestone', params: { namespace: props.namespace, name: props.name, iid: String(m.iid) }, query: { tab: 'needs' } },
  }
})

// holdProps: one compact card per held issue (stream tab; the Issues tab has
// the Held lane, the Needs you tab the full cards), also while the milestone runs.
const holdProps = computed(() => {
  const m = milestone.value
  if (everything.value || !m || m.stored || !props.namespace || !props.name) return []
  const to = { name: 'stream-milestone', params: { namespace: props.namespace, name: props.name, iid: String(m.iid) }, query: { tab: 'needs' } }
  return (m.autopilot?.holds ?? []).map((h) => ({ kind: h.kind, reason: h.reason, issue: h.iid, hold: true, milestoneTitle: m.title, exits: h.exits, to }))
})

// milestoneTasks: the Board of a milestone stream, as the milestone page
// shows it (the project's copy until the milestone loaded).
const milestoneTasks = computed(() => {
  const m = milestone.value
  if (m && m.iid === milestoneIID.value) return m.tasks
  return project.value?.milestones.find((x) => x.iid === milestoneIID.value)?.tasks ?? []
})

const items = computed(() => groupThreads(events.value))

const heatDays = computed(() => {
  const days = history.value?.days ?? []
  if (!days.length) return []
  const max = Math.max(1, ...days.map((d) => d.events))
  return days.map((d) => ({ ...d, level: heatLevel(d.events, max) }))
})

const heatRange = computed(() => {
  const days = heatDays.value
  if (!days.length) return { start: '', end: 'today' }
  const first = days[0]!.day
  const start = new Date(first + 'T12:00:00')
  return {
    start: start.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }),
    end: 'today',
  }
})

function streamQuery(extra: Record<string, string | undefined> = {}): URLSearchParams {
  const params = new URLSearchParams()
  if (!everything.value) params.set('project', `${props.namespace}/${props.name}`)
  if (milestoneIID.value) params.set('milestone', String(milestoneIID.value))
  // Chat kinds collide with history ids: only with the chat feature, and
  // only when "all" names them explicitly (other chips unchanged).
  const kinds = milestoneIID.value ? runtimeKindsForFilter(filter.value) : kindsForFilter(filter.value, hasFeature('chat'))
  if (kinds) params.set('kinds', kinds.join(','))
  if (q.value.trim()) params.set('q', q.value.trim())
  for (const [k, v] of Object.entries(extra)) {
    if (v) params.set(k, v)
  }
  params.set('limit', '50')
  return params
}

async function loadInitial() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  notFound.value = false
  try {
    const page = await getStream(
      streamQuery(beforeJump.value ? { before: beforeJump.value } : {}),
    )
    if (mine !== seq) return
    events.value = page.events
    older.value = page.older
    newest.value = page.newest
    historyOn.value = page.history
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return
    if (e instanceof NotFoundError) notFound.value = true
    else error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

async function loadOlder() {
  if (!older.value || loadingOlder.value) return
  loadingOlder.value = true
  try {
    const page = await getStream(streamQuery({ before: older.value }))
    events.value = [...events.value, ...page.events]
    older.value = page.older
  } catch (e) {
    if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loadingOlder.value = false
  }
}

async function loadLive() {
  if (!newest.value) return
  try {
    const page = await getStream(streamQuery({ after: newest.value }))
    if (page.events.length) {
      const seen = new Set(events.value.map(streamEventKey))
      const fresh = page.events.filter((e) => !seen.has(streamEventKey(e)))
      if (fresh.length) events.value = [...fresh, ...events.value]
      if (page.newest) newest.value = page.newest
    }
  } catch {
    /* live refresh is best-effort */
  }
}

async function loadHistory() {
  if (everything.value) {
    history.value = null
    return
  }
  try {
    history.value = await getProjectHistory(props.namespace!, props.name!, 30, Intl.DateTimeFormat().resolvedOptions().timeZone)
  } catch (e) {
    if (e instanceof NotFoundError) history.value = null
  }
}

async function loadMilestone() {
  const iid = milestoneIID.value
  if (everything.value) {
    milestone.value = null
    return
  }
  // Prefer scoped milestone; else first with autopilot.
  let pick = iid
  if (!pick && project.value?.milestones.length) {
    const ap = project.value.milestones.find((m) => m.spec?.status.autopilot?.phase === 'Running')
      ?? project.value.milestones.find((m) => m.spec?.status.autopilot)
      ?? project.value.milestones[0]
    pick = ap?.iid
  }
  if (!pick) {
    milestone.value = null
    return
  }
  try {
    milestone.value = await getMilestone(props.namespace!, props.name!, pick)
    milestoneFailed.value = false
  } catch {
    milestone.value = null
    milestoneFailed.value = !!iid
  }
}

function jumpDay(day: string) {
  beforeJump.value = dayEndCursor(day)
  void loadInitial()
}

function clearJump() {
  if (!beforeJump.value) return
  beforeJump.value = undefined
  void loadInitial()
}

function applySearch() {
  q.value = qDraft.value
  beforeJump.value = undefined
  void loadInitial()
}

function setFilter(id: StreamFilter) {
  filter.value = id
  beforeJump.value = undefined
  void loadInitial()
}

const title = computed(() => {
  if (milestoneIID.value && milestone.value) return milestone.value.title
  return props.name ?? 'Stream'
})

// status: a milestone's one status, the header's eyebrow (decision 4); a
// project has none.
const status = computed(() => {
  const m = milestone.value
  return milestoneIID.value && m?.iid === milestoneIID.value ? milestoneStatus(m) : null
})

// metricsLink: a project's Metrics, filtered to it, with the history database.
const metricsLink = computed(() =>
  !everything.value && !milestoneIID.value && props.namespace && props.name && hasFeature('history')
    ? { path: '/metrics', query: { project: `${props.namespace}/${props.name}` } }
    : undefined,
)

// headerCost is the cost chip of the stream's project or milestone.
const headerCost = computed((): Cost | null => {
  if (everything.value) return null
  const iid = milestoneIID.value
  if (!iid) return project.value
  if (milestone.value?.iid === iid) return milestone.value
  return project.value?.milestones.find((m) => m.iid === iid) ?? null
})

// The breadcrumbs name the milestone by the project's list, or the
// milestone once loaded (R19).
watchEffect(() => {
  const iid = milestoneIID.value
  const p = project.value
  if (!iid || !props.namespace || !props.name || p?.namespace !== props.namespace || p.name !== props.name) return
  const m = milestone.value?.iid === iid ? milestone.value : p.milestones.find((x) => x.iid === iid)
  if (m) setCrumbNames({ milestone: { namespace: props.namespace, name: props.name, iid, title: m.title } })
})

watch(
  () => [props.namespace, props.name, props.iid] as const,
  () => {
    beforeJump.value = undefined
    if (!filters.value.some((f) => f.id === filter.value)) filter.value = 'all'
    void loadProject()
    void loadInitial()
    void loadHistory()
  },
  { immediate: true },
)

watch(
  () => [project.value, milestoneIID.value] as const,
  () => void loadMilestone(),
)

watch(
  () => live.version,
  () => {
    if (!live.kinds || live.kinds.some((k) => k === 'project' || k === 'task')) {
      void loadLive()
      void loadProject()
      void loadMilestone()
    }
  },
)

function showDay(i: number): string | null {
  const list = items.value
  const cur = list[i]!
  const at = cur.kind === 'thread' ? cur.at : cur.event.at
  if (i === 0) return dayLabel(at)
  const prev = list[i - 1]!
  const prevAt = prev.kind === 'thread' ? prev.at : prev.event.at
  return sameLocalDay(at, prevAt) ? null : dayLabel(at)
}

</script>

<template>
  <NotFound v-if="notFound" />
  <ContextLayout
    v-else
    v-model:open="contextOpen"
    :column="!milestoneIID"
    description="Needs you, the milestone's queue and companions, the last 30 days"
  >
    <template #default="{ sheet }">
    <div class="stream">
      <!-- The page header (decision 4): the milestone's status as eyebrow,
           the title, the cost as meta line, Metrics (a project) and Context
           (below 1180 px) on the right. The tab strip follows; on phones it
           and the search take a full row each, from 760 px they share one
           row, the search on the right, shrinking from 180 px to 64 px
           before it would wrap. On the dashboard's Stream tab (everything)
           the dashboard has the header. -->
      <div data-stream-head class="bg-(--bg) px-4 pt-3.5 tablet:px-7">
        <UPageHeader v-if="!everything" :title="title" :ui="pageHeaderUi">
          <template v-if="status" #headline>
            <span :class="toneText[status.tone]" data-stream-status>{{ status.label }}</span>
          </template>
          <template v-if="headerCost" #description><CostChip :cost="headerCost" /></template>
          <template v-if="metricsLink || sheet" #links>
            <UButton
              v-if="metricsLink"
              color="neutral"
              variant="outline"
              icon="i-lucide-receipt"
              label="Metrics"
              :to="metricsLink"
              data-project-metrics
            />
            <UButton
              v-if="sheet"
              color="neutral"
              variant="outline"
              icon="i-lucide-panel-right"
              label="Context"
              aria-haspopup="dialog"
              :aria-expanded="contextOpen"
              :aria-label="projectInbox.length ? `Context, ${projectInbox.length} need you` : 'Context'"
              @click="contextOpen = true"
            >
              <template v-if="projectInbox.length" #trailing>
                <Badge tone="warning">{{ projectInbox.length }}</Badge>
              </template>
            </UButton>
          </template>
        </UPageHeader>
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <RouteTabs
            v-if="!everything"
            class="basis-full tablet:basis-auto"
            :tabs="tabs"
            :active="tab"
            :to="(id) => ({ query: tabQuery(route.query, id, tabs) })"
          />
          <span class="hidden flex-1 tablet:block" />
          <form
            v-if="tab === 'stream'"
            role="search"
            class="basis-full tablet:max-w-45 tablet:grow tablet:basis-16"
            @submit.prevent="applySearch"
          >
            <UInput
              v-model="qDraft"
              type="search"
              placeholder="Search…"
              aria-label="Search stream"
              icon="i-lucide-search"
              class="w-full"
              :ui="{ base: 'tablet:text-sm/5' }"
            />
          </form>
        </div>
        <div v-if="tab === 'stream' && heatDays.length" class="heat" title="Activity per day; click to jump">
          <span>{{ heatRange.start }}</span>
          <div class="cells">
            <button
              v-for="d in heatDays"
              :key="d.day"
              type="button"
              :data-l="d.level"
              :title="`${d.day} · ${d.events} events`"
              :class="{ sel: beforeJump === dayEndCursor(d.day) }"
              @click="jumpDay(d.day)"
            />
          </div>
          <button v-if="beforeJump" type="button" class="clear" @click="clearJump">clear</button>
          <span v-else>{{ heatRange.end }}</span>
        </div>
      </div>

      <div v-if="tab === 'stream'" class="scroll">
        <!-- A project's milestones (R9), in the dashboard's lists. -->
        <div v-if="!everything && !milestoneIID && namespace && name" class="milestones">
          <ProjectQueue
            v-if="project"
            class="mb-3"
            :namespace="namespace"
            :name="name"
            :queue="project.queue ?? []"
            :commands="project.queueCommands"
          />
          <ProjectFinishing v-if="project" class="mb-3" :namespace="namespace" :name="name" :finishing="project.finishing ?? []" :gitlab="project.gitlab.url" />
          <MilestoneSections :namespace="namespace" :name="name" />
        </div>
        <div v-if="!everything && namespace && name" class="pinned">
          <PauseCardCompact v-if="pauseProps" class="pause" v-bind="pauseProps" />
          <PauseCardCompact v-for="h in holdProps" :key="h.issue" class="pause" v-bind="h" />
          <!-- A milestone's composer is on its Pilot tab. -->
          <Composer
            v-if="!milestoneIID"
            v-model="note.body"
            v-model:target="note.target"
            mode="note"
            :placeholder="note.placeholder"
            :hint="note.hint"
            :busy="note.sending"
            :disabled="!!note.disabledReason"
            :error="note.error"
            :targets="note.targets"
            :suggestions="slashSuggestions"
            :blocked="note.blocked"
            :connect-url="note.connectUrl"
            @send="note.send"
          />
        </div>
        <p v-else class="pick muted">Pick a project in the rail to message a pilot or issue.</p>

        <div class="filters">
          <button
            v-for="f in filters"
            :key="f.id"
            type="button"
            class="chip"
            :class="{ on: filter === f.id }"
            @click="setFilter(f.id)"
          >
            {{ f.label }}
          </button>
        </div>

        <div v-if="!historyOn" class="banner info">History needs the research database.</div>
        <div v-if="error" class="banner danger">{{ error }}</div>
        <div v-if="loading && !events.length" class="empty">Loading…</div>
        <UEmpty v-else-if="!events.length" icon="i-lucide-history" title="No events yet." variant="naked" size="sm" />

        <template
          v-for="(item, i) in items"
          :key="item.kind === 'thread' ? item.key : streamEventKey(item.event)"
        >
          <div v-if="showDay(i)" class="day">{{ showDay(i) }}</div>
          <EventCard
            v-if="item.kind === 'thread'"
            :notes="item.notes"
            :project="project"
            :can-steer="canSteer"
            @posted="loadInitial"
          />
          <EventCard
            v-else
            :event="item.event"
            :project="project"
            :prompts="milestone?.prompts"
            :can-steer="canSteer"
            @posted="loadInitial"
          />
        </template>

        <div v-if="older" class="mt-[18px] flex justify-center">
          <UButton
            color="neutral"
            variant="outline"
            size="sm"
            :disabled="loadingOlder"
            :label="loadingOlder ? 'Loading…' : 'Load older'"
            @click="loadOlder"
          />
        </div>
      </div>
      <div v-else-if="tab === 'issues'" class="scroll board">
        <!-- The full pause card is on the Needs you tab. -->
        <PauseCardCompact v-if="pauseProps" class="pause" v-bind="pauseProps" />
        <MilestoneIssues
          v-if="namespace && name && milestone && milestone.iid === milestoneIID"
          :milestone="milestone"
          :namespace="namespace"
          :name="name"
        />
        <div v-else-if="milestoneFailed" class="banner danger">The milestone could not be loaded.</div>
        <div v-else class="empty">Loading…</div>
      </div>
      <div v-else-if="tab === 'needs'" class="scroll board">
        <MilestoneNeeds
          v-if="namespace && name && milestone && milestone.iid === milestoneIID"
          :namespace="namespace"
          :name="name"
          :milestone="milestone"
        />
        <div v-else-if="milestoneFailed" class="banner danger">The milestone could not be loaded.</div>
        <div v-else class="empty">Loading…</div>
      </div>
      <div v-else-if="tab === 'decisions'" class="scroll board">
        <!-- From the history database, so also on stored milestones. -->
        <MilestoneDecisions v-if="namespace && name && milestoneIID" :namespace="namespace" :name="name" :milestone="milestoneIID" />
        <MilestoneDecisions
          v-else-if="namespace && name"
          :namespace="namespace"
          :name="name"
          :milestone="decisionsMilestone"
          :milestones="decisionsMilestones"
        >
          <template #controls>
            <USelect
              :model-value="decisionsMilestone"
              :items="decisionsMilestoneItems"
              aria-label="Milestone"
              trailing-icon="i-lucide-chevron-down"
              selected-icon="i-lucide-check"
              class="min-h-11 w-full tablet:min-h-0 tablet:w-auto tablet:min-w-44"
              :ui="{ base: 'tablet:text-sm/5', item: 'min-h-11 tablet:min-h-0' }"
              data-decisions-milestone
              @update:model-value="setDecisionsMilestone(Number($event))"
            />
          </template>
        </MilestoneDecisions>
      </div>
      <div v-else-if="tab === 'pilot'" class="scroll board">
        <MilestonePilot
          v-if="namespace && name && milestone && milestone.iid === milestoneIID"
          :namespace="namespace"
          :name="name"
          :milestone="milestone"
          :project="project"
        />
        <div v-else-if="milestoneFailed" class="banner danger">The milestone could not be loaded.</div>
        <div v-else class="empty">Loading…</div>
      </div>
      <div v-else-if="tab === 'retrospective'" class="scroll board">
        <MilestoneRetro
          v-if="namespace && name && milestone && milestone.iid === milestoneIID"
          :namespace="namespace"
          :name="name"
          :milestone="milestone"
        />
        <div v-else-if="milestoneFailed" class="banner danger">The milestone could not be loaded.</div>
        <div v-else class="empty">Loading…</div>
      </div>
      <div v-else-if="tab === 'plans'" class="scroll board">
        <!-- The project's plans (R13); a 404 (no chat) shows an empty list. -->
        <PlanList v-if="namespace && name" :project="{ namespace, name }" />
      </div>
      <div v-else-if="tab === 'metrics'" class="scroll board">
        <SpendPanel
          v-if="namespace && name"
          :namespace="namespace"
          :name="name"
          :milestone-iid="milestoneIID"
          :milestones="project?.milestones ?? []"
        />
      </div>
      <div v-else class="scroll board">
        <template v-if="namespace && name">
          <Board v-if="milestoneIID" :tasks="milestoneTasks" />
          <ProjectBoards v-else-if="project" :namespace="namespace" :name="name" :project="project" />
        </template>
        <div v-if="error" class="banner danger">{{ error }}</div>
        <div v-if="!project && !error" class="empty">Loading…</div>
      </div>
    </div>
    </template>

    <!-- A project's or a milestone's context: a column from 1180 px on
         project pages, a sheet below on both. -->
    <template v-if="hasContext" #panel="{ sheet }">
      <ContextPanel
        :class="{ 'ctx-sheet': sheet }"
        :namespace="namespace!"
        :name="name!"
        :inbox="projectInbox"
        :milestone="milestone"
        :history="history"
      />
    </template>
  </ContextLayout>
</template>

<style scoped>
.stream {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  height: 100%;
}
.heat {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 0 0;
  font-size: 11.5px;
  color: var(--text-3);
}
.cells {
  display: flex;
  gap: 3px;
  flex: 1;
}
.cells button {
  flex: 1;
  height: 16px;
  border-radius: 3px;
  background: var(--surface-3);
  border: 0;
  cursor: pointer;
  padding: 0;
}
.cells button[data-l='1'] {
  background: color-mix(in srgb, var(--tone-progress-fg) 25%, var(--surface-3));
}
.cells button[data-l='2'] {
  background: color-mix(in srgb, var(--tone-progress-fg) 50%, var(--surface-3));
}
.cells button[data-l='3'] {
  background: color-mix(in srgb, var(--tone-progress-fg) 80%, var(--surface-3));
}
.cells button.sel {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.clear {
  border: 0;
  background: none;
  color: var(--link);
  font: inherit;
  font-size: 11.5px;
  cursor: pointer;
}
.scroll {
  overflow-y: auto;
  flex: 1;
  padding: 0 28px 40px;
}
.scroll.board {
  padding-top: 18px;
}
/* The pause card and the composer stay on top while the stream scrolls. */
.pinned {
  position: sticky;
  top: 0;
  z-index: 4;
  background: var(--bg);
}
.milestones {
  padding-top: 4px;
}
.pinned .pause {
  margin-top: 14px;
}
.filters {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  margin-top: 10px;
  margin-bottom: 4px;
}
.chip {
  border: 1px solid var(--border);
  background: var(--surface);
  padding: 3px 10px;
  border-radius: 999px;
  font-size: 12.5px;
  color: var(--text-2);
  cursor: pointer;
  font: inherit;
}
.chip.on {
  background: var(--text);
  color: var(--bg);
  border-color: var(--text);
}
.day {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 11.5px;
  font-weight: 600;
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  margin: 22px 0 10px;
}
.day::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.pick {
  padding: 14px 0 4px;
  font-size: 13.5px;
}
/* In the sheet the panel needs no edge of its own; the sheet's body scrolls. */
.ctx-sheet {
  border-left: 0;
  overflow-y: visible;
}
@media (max-width: 759.98px) {
  .scroll {
    padding-left: 16px;
    padding-right: 16px;
  }
  .heat {
    display: none;
  }
}
</style>
