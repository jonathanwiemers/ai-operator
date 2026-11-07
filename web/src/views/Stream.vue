<script setup lang="ts">
import { computed, ref, watch, watchEffect } from 'vue'
import { useRoute } from 'vue-router'
import { get, hasFeature, LoginRequiredError, NotFoundError } from '../api'
import { setCrumbNames } from '../crumbNames'
import Board from '../components/Board.vue'
import ContextPanel from '../components/ContextPanel.vue'
import CostChip from '../components/CostChip.vue'
import PauseCardCompact from '../components/PauseCardCompact.vue'
import ProjectBoards from '../components/ProjectBoards.vue'
import SpendPanel from '../components/SpendPanel.vue'
import StreamComposer from '../components/StreamComposer.vue'
import EventCard from '../components/stream/EventCard.vue'
import MilestoneIssues from '../components/MilestoneIssues.vue'
import MilestonePilot from '../components/MilestonePilot.vue'
import MilestoneSections from '../components/MilestoneSections.vue'
import { live, useResource } from '../live'
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
  streamEventKey,
  streamFilters,
  type StreamFilter,
} from '../stream'
import type {
  Cost,
  HistoryResponse,
  InboxResponse,
  MilestoneView,
  ProjectDetail,
  StreamEvent,
  StreamPage,
} from '../types'
import { carryTab, milestoneTabFromRoute, milestoneTabs, projectTabs, storedMilestoneTabs, tabFromQuery, tabQuery } from '../streamTabs'
import NotFound from './NotFound.vue'

const props = defineProps<{
  namespace?: string
  name?: string
  iid?: string
}>()

const everything = computed(() => !props.namespace || !props.name)
const milestoneIID = computed(() => (props.iid ? Number(props.iid) : undefined))

// tab: Stream, Board or Spend on project streams, Issues (the default),
// Pilot, Stream, Board or Spend on milestones, kept in ?tab= so it can be
// linked to (a #prompt-Q<n> link without it opens Pilot); Everything has no
// tabs. A stored milestone has no tasks, so no Board.
const route = useRoute()
const tabs = computed(() => {
  if (everything.value) return []
  if (!milestoneIID.value) return projectTabs
  return milestone.value?.iid === milestoneIID.value && milestone.value.stored ? storedMilestoneTabs : milestoneTabs
})
const tab = computed((): string => {
  if (everything.value) return 'stream'
  if (milestoneIID.value) return milestoneTabFromRoute(route.query.tab, route.hash, tabs.value)
  return tabFromQuery(route.query.tab, tabs.value)
})
// scopeQuery is a scope link's query: it keeps a chosen tab where the
// target has it, else opens the target's default.
function scopeQuery(milestone: boolean) {
  const to = milestone ? milestoneTabs : projectTabs
  return tabQuery({}, carryTab(tab.value, tabs.value, to), to)
}

const filter = ref<StreamFilter>('all')
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

const projectPath = computed(() =>
  everything.value
    ? ''
    : `/projects/${encodeURIComponent(props.namespace!)}/${encodeURIComponent(props.name!)}`,
)

const project = ref<ProjectDetail | null>(null)

async function loadProject() {
  if (!projectPath.value) {
    project.value = null
    return
  }
  try {
    project.value = await get<ProjectDetail>(projectPath.value)
  } catch (e) {
    if (e instanceof NotFoundError) {
      notFound.value = true
      project.value = null
    } else if (!(e instanceof LoginRequiredError)) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  }
}

const { data: inbox } = useResource<InboxResponse>(() => '/inbox', ['project', 'task'])

const history = ref<HistoryResponse | null>(null)
const milestone = ref<MilestoneView | null>(null)
// milestoneFailed: the scoped milestone could not be loaded (the Issues tab
// says so instead of loading forever).
const milestoneFailed = ref(false)

const projectInbox = computed(() => {
  const items = inbox.value?.items ?? []
  if (everything.value) return items
  return items.filter((i) => i.namespace === props.namespace && i.project === props.name)
})

const canSteer = computed(() => !!milestone.value?.permissions?.steer)

// paused: the loaded milestone's paused autopilot, pinned above the composer
// (project and milestone streams only; the Inbox covers Everything).
const paused = computed(() => {
  const m = milestone.value
  return !everything.value && m?.autopilot?.phase === 'Paused' ? m : null
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

function streamQuery(extra: Record<string, string | undefined> = {}): string {
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
  const s = params.toString()
  return `/stream?${s}`
}

async function loadInitial() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  notFound.value = false
  try {
    const page = await get<StreamPage>(
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
    const page = await get<StreamPage>(streamQuery({ before: older.value }))
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
    const page = await get<StreamPage>(streamQuery({ after: newest.value }))
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
    history.value = await get<HistoryResponse>(
      `${projectPath.value}/history?days=30&tz=${encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone)}`,
    )
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
    milestone.value = await get<MilestoneView>(
      `${projectPath.value}/milestones/${pick}`,
    )
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
  if (everything.value) return 'Everything'
  if (milestoneIID.value && milestone.value) return milestone.value.title
  return props.name ?? 'Stream'
})

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
  <div v-else class="desk" :class="{ wide: milestoneIID }">
    <div class="stream">
      <div class="head">
        <div v-if="milestone?.autopilot" class="apline">
          <span class="ap">Autopilot {{ milestone.autopilot.phase.toLowerCase() }}</span>
        </div>
        <div class="title-row">
          <h1>{{ title }}</h1>
          <CostChip :cost="headerCost" />
          <nav v-if="!everything" class="tabs" aria-label="View">
            <RouterLink
              v-for="t in tabs"
              :key="t.id"
              :to="{ query: tabQuery(route.query, t.id, tabs) }"
              :class="{ on: tab === t.id }"
              :aria-current="tab === t.id ? 'page' : undefined"
            >
              {{ t.label }}
            </RouterLink>
          </nav>
          <span class="spacer" />
          <form v-if="tab === 'stream'" class="search" @submit.prevent="applySearch">
            <input v-model="qDraft" type="search" placeholder="Search…" aria-label="Search stream" />
          </form>
        </div>
        <div v-if="!everything" class="scope">
          <RouterLink
            :to="{ name: 'stream-project', params: { namespace, name }, query: scopeQuery(false) }"
            :class="{ on: !milestoneIID }"
          >
            Everything
          </RouterLink>
          <RouterLink
            v-for="m in project?.milestones ?? []"
            :key="m.iid"
            :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(m.iid) }, query: scopeQuery(true) }"
            :class="{ on: milestoneIID === m.iid }"
          >
            {{ m.title }}
            <span v-if="m.spec?.status.autopilot" class="n">{{ m.spec.status.autopilot.phase === 'Done' ? 'done' : '' }}</span>
            <CostChip class="cost" :cost="m" small />
          </RouterLink>
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
          <MilestoneSections :namespace="namespace" :name="name" />
        </div>
        <div v-if="!everything && namespace && name" class="pinned">
          <PauseCardCompact
            v-if="paused"
            class="pause"
            :kind="paused.autopilot?.pauseKind"
            :reason="paused.autopilot?.pausedReason"
            :issue="paused.autopilot?.pausedFor"
            :milestone-title="paused.title"
            :waiting="paused.autopilot?.waitingFor"
            :exits="paused.autopilot?.exits"
            :prompt="pausePrompt(paused.autopilot?.pauseKind, paused.prompts)"
            :namespace="namespace"
            :project-name="name"
            :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(paused.iid) }, query: { tab: 'pilot' } }"
          />
          <!-- A milestone's composer is on its Pilot tab. -->
          <StreamComposer
            v-if="!milestoneIID"
            :namespace="namespace"
            :name="name"
            :project="project"
            :milestone-iid="milestoneIID"
            :steer="milestone?.permissions?.steer"
            @posted="loadInitial"
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
        <div v-else-if="!events.length" class="empty">No events yet.</div>

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

        <div v-if="older" class="older">
          <button type="button" class="btn" :disabled="loadingOlder" @click="loadOlder">
            {{ loadingOlder ? 'Loading…' : 'Load older' }}
          </button>
        </div>
      </div>
      <div v-else-if="tab === 'issues'" class="scroll board">
        <MilestoneIssues
          v-if="namespace && name && milestone && milestone.iid === milestoneIID"
          :milestone="milestone"
          :namespace="namespace"
          :name="name"
        />
        <div v-else-if="milestoneFailed" class="banner danger">The milestone could not be loaded.</div>
        <div v-else class="empty">Loading…</div>
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
      <div v-else-if="tab === 'spend'" class="scroll board">
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

    <ContextPanel
      v-if="!everything && namespace && name"
      class="ctx-wrap"
      :namespace="namespace"
      :name="name"
      :full="!!milestoneIID && tab !== 'pilot'"
      :inbox="projectInbox"
      :milestone="milestone"
      :history="history"
    />
  </div>
</template>

<style scoped>
.desk {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 320px;
  min-height: calc(100vh - 0px);
  height: 100%;
  min-width: 0;
}
/* A milestone's panel holds the pause card and the pilot chat. */
.desk.wide {
  grid-template-columns: minmax(0, 1fr) 380px;
}
.stream {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  height: 100%;
}
.head {
  padding: 14px 28px 0;
  background: var(--bg);
}
.apline {
  font-size: 12.5px;
  color: var(--text-3);
  display: flex;
  gap: 6px;
  align-items: center;
}
.ap {
  color: var(--tone-progress-fg);
  font-weight: 600;
}
.title-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 2px;
}
.title-row h1 {
  font-size: 20px;
  margin: 0;
  letter-spacing: -0.01em;
}
.tabs {
  display: flex;
  gap: 2px;
  padding: 2px;
  border-radius: var(--radius-sm);
  background: var(--surface-3);
}
.tabs a {
  padding: 3px 12px;
  border-radius: 5px;
  color: var(--text-2);
  font-size: 13px;
  font-weight: 500;
}
.tabs a:hover {
  text-decoration: none;
  color: var(--text);
}
.tabs a.on {
  background: var(--surface);
  color: var(--text);
  box-shadow: var(--shadow);
}
.search input {
  height: 32px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  padding: 0 10px;
  font: inherit;
  color: var(--text);
  width: 180px;
}
.scope {
  display: flex;
  gap: 2px;
  margin-top: 12px;
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
}
.scope a {
  padding: 8px 12px;
  color: var(--text-2);
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  font-weight: 500;
  white-space: nowrap;
}
.scope a:hover {
  text-decoration: none;
  color: var(--text);
}
.scope a.on {
  color: var(--text);
  border-color: var(--accent);
}
.scope a .cost {
  margin-left: 6px;
}
.scope a .n {
  font-size: 11px;
  color: var(--text-3);
  margin-left: 4px;
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
.older {
  display: flex;
  justify-content: center;
  margin-top: 18px;
}
.btn {
  border: 1px solid var(--border-strong);
  background: var(--surface);
  padding: 6px 14px;
  border-radius: 7px;
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}
.pick {
  padding: 14px 0 4px;
  font-size: 13.5px;
}
.empty {
  padding: 32px;
  text-align: center;
  color: var(--text-3);
}
@media (max-width: 1180px) {
  .desk,
  .desk.wide {
    grid-template-columns: minmax(0, 1fr);
  }
  .ctx-wrap {
    display: none;
  }
}
@media (max-width: 760px) {
  .head,
  .scroll {
    padding-left: 16px;
    padding-right: 16px;
  }
  .heat {
    display: none;
  }
  .search input {
    width: 120px;
  }
}
</style>
