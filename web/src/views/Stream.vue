<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { get, LoginRequiredError, NotFoundError } from '../api'
import ContextPanel from '../components/ContextPanel.vue'
import StreamComposer from '../components/StreamComposer.vue'
import EventCard from '../components/stream/EventCard.vue'
import { live, useResource } from '../live'
import {
  dayEndCursor,
  dayLabel,
  groupThreads,
  heatLevel,
  kindsForFilter,
  sameLocalDay,
  streamFilters,
  type StreamFilter,
} from '../stream'
import type {
  HistoryResponse,
  InboxResponse,
  MilestoneView,
  ProjectDetail,
  StreamEvent,
  StreamPage,
} from '../types'
import NotFound from './NotFound.vue'

const props = defineProps<{
  namespace?: string
  name?: string
  iid?: string
}>()

const everything = computed(() => !props.namespace || !props.name)
const milestoneIID = computed(() => (props.iid ? Number(props.iid) : undefined))

const filter = ref<StreamFilter>('all')
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

const projectInbox = computed(() => {
  const items = inbox.value?.items ?? []
  if (everything.value) return items
  return items.filter((i) => i.namespace === props.namespace && i.project === props.name)
})

const canSteer = computed(() => !!milestone.value?.permissions?.steer)

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
  const kinds = kindsForFilter(filter.value)
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
      const seen = new Set(events.value.map((e) => e.id))
      const fresh = page.events.filter((e) => !seen.has(e.id))
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
  } catch {
    milestone.value = null
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

const crumbs = computed(() => {
  if (everything.value) return 'All projects'
  return `${props.namespace} / ${props.name}`
})

watch(
  () => [props.namespace, props.name, props.iid] as const,
  () => {
    beforeJump.value = undefined
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
  <div v-else class="desk">
    <div class="stream">
      <div class="head">
        <div class="crumbs">
          {{ crumbs }}
          <template v-if="milestone?.autopilot">
            <span>·</span>
            <span class="ap">Autopilot {{ milestone.autopilot.phase.toLowerCase() }}</span>
          </template>
        </div>
        <div class="title-row">
          <h1>{{ title }}</h1>
          <span class="spacer" />
          <form class="search" @submit.prevent="applySearch">
            <input v-model="qDraft" type="search" placeholder="Search…" aria-label="Search stream" />
          </form>
        </div>
        <div v-if="!everything" class="scope">
          <RouterLink
            :to="{ name: 'stream-project', params: { namespace, name } }"
            :class="{ on: !milestoneIID }"
          >
            Everything
          </RouterLink>
          <RouterLink
            v-for="m in project?.milestones ?? []"
            :key="m.iid"
            :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(m.iid) } }"
            :class="{ on: milestoneIID === m.iid }"
          >
            {{ m.title }}
            <span v-if="m.spec?.status.autopilot" class="n">{{ m.spec.status.autopilot.phase === 'Done' ? 'done' : '' }}</span>
          </RouterLink>
          <RouterLink
            :to="{ name: 'project-board', params: { namespace, name } }"
            class="board-link"
          >
            Board
          </RouterLink>
        </div>
        <div v-if="heatDays.length" class="heat" title="Activity per day; click to jump">
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

      <div class="scroll">
        <StreamComposer
          v-if="!everything && namespace && name"
          :namespace="namespace"
          :name="name"
          :project="project"
          :milestone-iid="milestoneIID"
          :steer="milestone?.permissions?.steer"
          @posted="loadInitial"
        />
        <p v-else class="pick muted">Pick a project in the rail to message a pilot or issue.</p>

        <div class="filters">
          <button
            v-for="f in streamFilters"
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

        <template v-for="(item, i) in items" :key="item.kind === 'thread' ? item.key : item.event.id">
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
    </div>

    <ContextPanel
      v-if="!everything"
      class="ctx-wrap"
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
.crumbs {
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
  .desk {
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
