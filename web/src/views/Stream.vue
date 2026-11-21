<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { computed, defineAsyncComponent, ref, watch, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import UButton from '@nuxt/ui/components/Button.vue'
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import USelect from '@nuxt/ui/components/Select.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import Badge from '../components/Badge.vue'
import { hasFeature } from '../api'
import { getInbox, getMilestone, getProject, getProjectHistory, getStream, LoginRequiredError, NotFoundError } from '../client'
import { setCrumbNames } from '../crumbNames'
import Board from '../components/Board.vue'
import ContextLayout from '../components/ContextLayout.vue'
import ContextPanel from '../components/ContextPanel.vue'
import CostChip from '../components/CostChip.vue'
import HeaderActions from '../components/HeaderActions.vue'
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
import DeskChat from '../ui/DeskChat.vue'
import RouteTabs from '../ui/RouteTabs.vue'
import { streamMessages } from '../chatView'
import { live, useResource } from '../live'
import { needsOpen } from '../needsOpen'
import { pausePrompt } from '../pauseCard'
import {
  dayEndCursor,
  groupThreads,
  heatCellLabel,
  heatLevel,
  heatSummary,
  kindsForFilter,
  runtimeKindsForFilter,
  runtimeStreamFilters,
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
import { alertColor, toneText } from '../badgeTone'
import { pageHeaderUi, searchToolbarUi } from '../pageHeader'
import { headerCost, keepCost, totalsCost } from '../headerCost'
import { useStreamNote } from '../streamNote'
import { decisionsQuery, milestoneFromQuery } from '../decisions'
import { milestoneTabFromRoute, milestoneTabs, milestoneTabsFor, pilotFromQuery, pilotQuery, projectTabsFor, tabFromQuery, tabQuery } from '../streamTabs'
import { useBackClose } from '../ui/useBackClose'
import NotFound from './NotFound.vue'
import { announceChange } from '../ui/useAnnounce'

// The queue and its drag library (vue-draggable-plus) load with the project
// page, not with the initial bundle (D662-P4).
const ProjectQueue = defineAsyncComponent(() => import('../components/ProjectQueue.vue'))

const props = defineProps<{
  namespace?: string
  name?: string
  iid?: string
  // problem: the problem route (Needs you tab with one thread open).
  problem?: string
}>()

const everything = computed(() => !props.namespace || !props.name)
const milestoneIID = computed(() => (props.iid ? Number(props.iid) : undefined))

// tab: Milestones (the default), Activity, Decisions, Board, Metrics or Plans (feature plans) on projects, Issues (the default),
// Activity, Decisions, Board, Metrics, Needs you or Retrospective on milestones, kept in ?tab= so it can be
// linked to (a #prompt-Q<n> link without it opens Needs you); Everything has no
// tabs. A stored milestone has no tasks, so no Board, a milestone shows Needs
// you only while something needs you and Retrospective only once it has one
// (milestoneTabsFor). The Needs you tab's badge is the count of its Open
// items (D674-P2). The pilot is a drawer (pilotOpen below), not a tab.
const route = useRoute()
// pageMilestone: the loaded milestone, once it is the route's.
const pageMilestone = computed(() => (milestone.value?.iid === milestoneIID.value ? milestone.value : null))
const needsCount = computed(() => (pageMilestone.value ? needsOpen(pageMilestone.value).count : 0))
const tabs = computed(() => {
  if (everything.value) return []
  if (!milestoneIID.value) return projectTabsFor(hasFeature('plans'))
  // A problem thread, or a #prompt- or #problem- link to one (MilestoneNeeds
  // redirects it to the thread), keeps the Needs you tab, also once handled.
  const problem = !!props.problem || /^#(prompt|problem)-/.test(route.hash)
  return milestoneTabsFor(pageMilestone.value, { needs: needsCount.value, problem })
})
const tabBadges = computed(() => (milestoneIID.value && needsCount.value ? { needs: needsCount.value } : undefined))
// A milestone's tab is read against all its tabs while it loads, so a
// ?tab=needs or ?tab=retrospective link waits for it; once loaded, a hidden
// tab falls back to the first one, Issues (Decision 7).
const tab = computed((): string => {
  if (everything.value) return 'activity'
  if (props.problem) return 'needs'
  if (milestoneIID.value) {
    const loaded = milestone.value?.iid === milestoneIID.value || milestoneFailed.value
    return milestoneTabFromRoute(route.query.tab, route.hash, loaded ? tabs.value : milestoneTabs)
  }
  return tabFromQuery(route.query.tab, tabs.value)
})
// tabTo: a tab's link; from a problem's thread it leads back to the
// milestone's route.
function tabTo(id: string) {
  const query = tabQuery(route.query, id, tabs.value)
  if (!props.problem) return { query }
  return { name: 'stream-milestone' as const, params: { namespace: props.namespace, name: props.name, iid: props.iid }, query }
}
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
// A milestone's Activity tab has the runtime kinds only; the pilot drawer the rest.
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

// The project's note composer (a milestone's is in its pilot drawer).
const note = useStreamNote(
  () => ({
    namespace: props.namespace ?? '',
    name: props.name ?? '',
    project: project.value,
    steer: milestone.value?.permissions?.steer,
    shown: tab.value === 'activity' && !everything.value && !milestoneIID.value,
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

// holdProps: one compact card per held issue (Activity tab; the Issues tab has
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

// The stream as a chat (DeskChat): oldest first, new events at the bottom.
const streamChat = computed(() => streamMessages(groupThreads(events.value)))

const heatDays = computed(() => {
  const days = history.value?.days ?? []
  if (!days.length) return []
  const max = Math.max(1, ...days.map((d) => d.events))
  return days.map((d) => ({ ...d, level: heatLevel(d.events, max) }))
})

// One tooltip for the whole heatmap (decision 7: no tooltip per cell),
// anchored to the cell under the pointer or with focus.
const heatHint = ref<{ el: HTMLElement; text: string } | null>(null)
const heatText = (d: { day: string; events: number }) => heatCellLabel(d.day, d.events)
const heatDescription = computed(() => heatSummary(heatDays.value))
function showHeatHint(e: Event) {
  const el = (e.target as HTMLElement | null)?.closest<HTMLElement>('[data-heat-day]')
  heatHint.value = el ? { el, text: el.getAttribute('aria-label') ?? '' } : null
}

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

// pageProject: the loaded project, once it is the route's (the old one stays
// in project while the next loads).
const pageProject = computed(() => {
  const p = project.value
  return p && p.namespace === props.namespace && p.name === props.name ? p : null
})

// title: the milestone's title from the loaded milestone, else from the
// project's list while it loads, else %<iid>; never the project's name.
const title = computed(() => {
  const iid = milestoneIID.value
  if (!iid) return props.name ?? 'Stream'
  return (pageMilestone.value ?? pageProject.value?.milestones.find((m) => m.iid === iid))?.title || `%${iid}`
})

// status: a milestone's one status, the header's eyebrow (decision 4); a
// project has none.
const status = computed(() => {
  const m = milestone.value
  return milestoneIID.value && m?.iid === milestoneIID.value ? milestoneStatus(m) : null
})

// A change of the open milestone's status is announced (useAnnounce.ts).
announceChange(() =>
  status.value ? { key: `${props.namespace}/${props.name}#${milestoneIID.value}`, text: `Milestone now ${status.value.label}` } : null,
)

// links: the header's ⋯ (D673-P2), the project or milestone in GitLab.
const links = computed(() => {
  const iid = milestoneIID.value
  if (!iid) return [{ label: 'Project in GitLab', href: pageProject.value?.gitlab.webURL }]
  const m = pageMilestone.value ?? pageProject.value?.milestones.find((x) => x.iid === iid)
  return [{ label: 'Milestone in GitLab', href: m?.webURL }]
})

// The pilot drawer (Decision 8): MilestonePilot in a sheet from the header's
// Ask pilot button, over any tab of a milestone. Its open state is
// ?pilot=1 (D674-P3), so a reload or a link keeps it open; an old
// ?tab=pilot link becomes Issues plus ?pilot=1. Opening sets the query and
// pilotOpen follows it; closing (Escape, close button, overlay, Back) pops
// useBackClose's entry, then drops ?pilot (onClose). Focus returns to the
// button. The sheet loads with its first opening.
const USlideover = defineAsyncComponent(() => import('@nuxt/ui/components/Slideover.vue'))
const pilotOpen = ref(false)
// pilotUsed: the drawer was opened once, so the sheet is loaded and mounted.
const pilotUsed = ref(false)
const pilotButton = ref<{ $el?: HTMLElement } | null>(null)
useBackClose(pilotOpen, {
  onClose: () => {
    if (pilotFromQuery(route.query)) void router.replace({ query: pilotQuery(route.query, false) })
  },
})
watch(
  () => [milestoneIID.value, route.query] as const,
  ([iid, query]) => {
    if (iid && query.tab === 'pilot') void router.replace({ query: pilotQuery(query, true) })
    pilotOpen.value = !!iid && pilotFromQuery(query)
    if (pilotOpen.value) pilotUsed.value = true
  },
  { immediate: true },
)
function openPilot() {
  void router.replace({ query: pilotQuery(route.query, true) })
}
function focusPilotButton(e: Event) {
  const el = pilotButton.value?.$el
  if (!el?.isConnected) return
  e.preventDefault()
  el.focus()
}

// The header's cost (headerCost.ts): one number per page, on every tab. The
// views' cost of the page's scope or the Metrics tab's totals of the same
// scope, the larger of the two sums from calls (a lagging copy never wins);
// a tasks-based fallback never replaces a cost from calls.
const costKey = computed(() => `${props.namespace}/${props.name}#${milestoneIID.value ?? ''}`)
const shownCost = ref<{ key: string; cost: Cost | null }>({ key: '', cost: null })
function showCost(next: Cost | null) {
  const prev = shownCost.value.key === costKey.value ? shownCost.value.cost : null
  shownCost.value = { key: costKey.value, cost: keepCost(prev, next) }
}
watch(
  () => (everything.value || !pageProject.value ? null : headerCost(pageProject.value, pageMilestone.value, milestoneIID.value)),
  showCost,
  { immediate: true },
)
const pageCost = computed(() => (!everything.value && shownCost.value.key === costKey.value ? shownCost.value.cost : null))

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
           the title, the cost as meta line (one number on every tab,
           headerCost.ts), Context (below 1180 px) and ⋯ with the links to
           GitLab on the right, then the tab row; the page's alerts are in
           the tabs. Filters, search and the heatmap are in the Activity
           tab's toolbar (decision 3). On the dashboard's Activity tab
           (everything) the dashboard has the header. -->
      <div data-stream-head class="bg-(--bg) px-4 pt-3.5 tablet:px-7">
        <UPageHeader v-if="!everything" :title="title" :ui="pageHeaderUi">
          <template v-if="status" #headline>
            <span :class="toneText[status.tone]" data-stream-status>{{ status.label }}</span>
          </template>
          <template v-if="pageCost" #description><CostChip :cost="pageCost" data-header-cost /></template>
          <template #links>
            <!-- The milestone's primary action (Ask pilot) goes first; a
                 stored milestone's drawer has the pilot's events only, so
                 its button says Pilot (D674-P4). -->
            <HeaderActions :actions="links" :shown="0">
              <UButton
                v-if="milestoneIID"
                ref="pilotButton"
                size="sm"
                class="min-h-11 justify-center tablet:min-h-0"
                icon="i-lucide-message-circle"
                :label="pageMilestone?.stored ? 'Pilot' : 'Ask pilot'"
                aria-haspopup="dialog"
                :aria-expanded="pilotOpen"
                data-ask-pilot
                @click="openPilot"
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
            </HeaderActions>
          </template>
        </UPageHeader>
        <RouteTabs v-if="!everything" :tabs="tabs" :active="tab" :to="tabTo" :badges="tabBadges" />
      </div>

      <!-- A project's milestones (R9), in the dashboard's lists: the
           project's default tab. -->
      <div v-if="tab === 'milestones'" class="scroll board">
        <div v-if="namespace && name" class="milestones">
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
      </div>
      <div v-else-if="tab === 'activity'" class="scroll stream">
        <!-- The tab's toolbar (decision 3): the event kind (UTabs pills)
             left, the search right; on phones the search takes a row of
             its own, last. The heatmap follows (hidden on phones). -->
        <UDashboardToolbar class="mt-3" :ui="searchToolbarUi" data-tab-toolbar>
          <template #left>
            <UTabs
              :model-value="filter"
              :items="filters.map((f) => ({ label: f.label, value: f.id }))"
              variant="pill"
              size="sm"
              :content="false"
              class="min-w-0 max-w-full"
              :ui="{ list: 'w-auto max-w-full overflow-x-auto' }"
              aria-label="Show events"
              data-stream-filter
              @update:model-value="setFilter($event as StreamFilter)"
            />
          </template>
          <template #right>
            <form role="search" class="w-full tablet:w-45" @submit.prevent="applySearch">
              <UInput
                v-model="qDraft"
                type="search"
                placeholder="Search…"
                aria-label="Search activity"
                icon="i-lucide-search"
                class="w-full"
                :ui="{ base: 'tablet:text-sm/5' }"
              />
            </form>
          </template>
        </UDashboardToolbar>
        <div v-if="heatDays.length" class="heat" title="Activity per day; click to jump">
          <span>{{ heatRange.start }}</span>
          <span class="sr-only" data-heat-summary>{{ heatDescription }}</span>
          <div class="cells" @pointerover="showHeatHint" @focusin="showHeatHint" @pointerleave="heatHint = null" @focusout="heatHint = null">
            <!-- A cell's look is the heatmap's (.cells button below), not a button's. -->
            <UButton
              v-for="d in heatDays"
              :key="d.day"
              color="neutral"
              variant="ghost"
              :data-l="d.level"
              :data-heat-day="d.day"
              :aria-label="heatText(d)"
              :class="{ sel: beforeJump === dayEndCursor(d.day) }"
              @click="jumpDay(d.day)"
            />
          </div>
          <UTooltip :open="!!heatHint" :reference="heatHint?.el" :text="heatHint?.text" />
          <UButton v-if="beforeJump" color="neutral" variant="link" size="xs" class="p-0 text-[11.5px] text-(--link)" label="clear" @click="clearJump" />
          <span v-else>{{ heatRange.end }}</span>
        </div>

        <UAlert v-if="!historyOn" :color="alertColor('info')" variant="subtle" class="mt-3" description="History needs the research database." />
        <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
        <div v-if="loading && !events.length" class="empty">Loading…</div>
        <DeskChat
          v-else
          class="min-h-[60dvh]"
          label="Activity"
          :messages="streamChat"
          :older="!!older"
          :loading-older="loadingOlder"
          empty="No events yet."
          @older="loadOlder"
        >
          <template #content="{ message }">
            <div v-if="message.metadata.day" class="day">{{ message.metadata.day }}</div>
            <EventCard
              v-if="message.metadata.notes"
              :notes="message.metadata.notes"
              :project="project"
              :can-steer="canSteer"
              @posted="loadInitial"
            />
            <EventCard
              v-else-if="message.metadata.event"
              :event="message.metadata.event"
              :project="project"
              :prompts="milestone?.prompts"
              :can-steer="canSteer"
              @posted="loadInitial"
            />
          </template>
          <template #bar>
            <template v-if="!everything && namespace && name">
              <PauseCardCompact v-if="pauseProps" v-bind="pauseProps" />
              <PauseCardCompact v-for="h in holdProps" :key="h.issue" v-bind="h" />
            </template>
          </template>
          <template #prompt>
            <!-- A milestone's composer is in its pilot drawer. -->
            <Composer
              v-if="!everything && namespace && name && !milestoneIID"
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
            <p v-else-if="everything || !namespace || !name" class="pick muted">Pick a project in the rail to message a pilot or issue.</p>
          </template>
        </DeskChat>
      </div>
      <div v-else-if="tab === 'issues'" class="scroll board">
        <!-- The full pause card is on the Needs you tab. -->
        <PauseCardCompact v-if="pauseProps" class="pause" v-bind="pauseProps" />
        <MilestoneIssues
          v-if="namespace && name && milestone && milestone.iid === milestoneIID"
          :milestone="milestone"
          :namespace="namespace"
          :name="name"
          :milestones="project?.milestones"
        />
        <UAlert v-else-if="milestoneFailed" :color="alertColor('danger')" variant="subtle" class="mt-3" description="The milestone could not be loaded." />
        <div v-else class="empty">Loading…</div>
      </div>
      <div v-else-if="tab === 'needs'" class="scroll board">
        <MilestoneNeeds
          v-if="namespace && name && milestone && milestone.iid === milestoneIID"
          :namespace="namespace"
          :name="name"
          :milestone="milestone"
          :problem="problem"
        />
        <UAlert v-else-if="milestoneFailed" :color="alertColor('danger')" variant="subtle" class="mt-3" description="The milestone could not be loaded." />
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
      <div v-else-if="tab === 'retrospective'" class="scroll board">
        <MilestoneRetro
          v-if="namespace && name && milestone && milestone.iid === milestoneIID"
          :namespace="namespace"
          :name="name"
          :milestone="milestone"
        />
        <UAlert v-else-if="milestoneFailed" :color="alertColor('danger')" variant="subtle" class="mt-3" description="The milestone could not be loaded." />
        <div v-else class="empty">Loading…</div>
      </div>
      <div v-else-if="tab === 'plans'" class="scroll board">
        <!-- The project's plans (R13); a 404 (no chat) shows an empty list. -->
        <PlanList v-if="namespace && name" :project="{ namespace, name }" :milestones="project?.milestones" />
      </div>
      <div v-else-if="tab === 'metrics'" class="scroll board">
        <SpendPanel
          v-if="namespace && name"
          :namespace="namespace"
          :name="name"
          :milestone-iid="milestoneIID"
          :milestones="project?.milestones ?? []"
          @totals="(t) => showCost(totalsCost(t))"
        />
      </div>
      <div v-else class="scroll board">
        <template v-if="namespace && name">
          <Board v-if="milestoneIID" :tasks="milestoneTasks" />
          <ProjectBoards v-else-if="project" :namespace="namespace" :name="name" :project="project" />
        </template>
        <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
        <div v-if="!project && !error" class="empty">Loading…</div>
      </div>

      <!-- The pilot drawer: from the right, the full width on phones. -->
      <USlideover
        v-if="milestoneIID && pilotUsed"
        v-model:open="pilotOpen"
        side="right"
        title="Pilot"
        :description="title"
        close-icon="i-lucide-x"
        :content="{ onCloseAutoFocus: focusPilotButton }"
        :ui="{
          overlay: 'z-40',
          content: 'z-40 w-full tablet:max-w-[560px] pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] pr-[env(safe-area-inset-right)]',
          header: 'min-h-0 gap-2 py-1 ps-4 pe-1 sm:ps-4 sm:pe-1',
          title: 'text-[15px] font-bold',
          description: 'truncate text-[13px]',
          close: 'static ms-auto',
        }"
        data-pilot-drawer
      >
        <template #body>
          <MilestonePilot v-if="namespace && name && pageMilestone" :namespace="namespace" :name="name" :milestone="pageMilestone" :project="project" />
          <UAlert v-else-if="milestoneFailed" :color="alertColor('danger')" variant="subtle" description="The milestone could not be loaded." />
          <div v-else class="empty">Loading…</div>
        </template>
      </USlideover>
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
        :milestones="project?.milestones"
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
  padding: 0 0 4px;
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
  min-width: 0;
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
.scroll {
  overflow-y: auto;
  flex: 1;
  padding: 0 28px 40px;
}
.scroll.board {
  padding-top: 18px;
}
/* The Activity tab is a chat: the feed scrolls, the composer stays below. */
.scroll.stream {
  display: flex;
  flex-direction: column;
  padding-bottom: 16px;
}
.milestones {
  padding-top: 4px;
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
