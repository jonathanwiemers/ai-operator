<script setup lang="ts">
import { errorMessage } from '../format'
import UAlert from '@nuxt/ui/components/Alert.vue'
import { computed, defineAsyncComponent, ref, watch, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import UButton from '@nuxt/ui/components/Button.vue'
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import { hasFeature } from '../api'
import { getInbox, getMilestone, getProject, getProjectHistory, LoginRequiredError, NotFoundError } from '../client'
import { setCrumbNames } from '../crumbNames'
import ContextLayout from '../components/ContextLayout.vue'
import ContextPanel from '../components/ContextPanel.vue'
import CostChip from '../components/CostChip.vue'
import HeaderActions from '../components/HeaderActions.vue'
import PauseCardCompact from '../components/PauseCardCompact.vue'
import ProjectBoards from '../components/ProjectBoards.vue'
import SpendPanel from '../components/SpendPanel.vue'
import Cockpit from '../components/cockpit/Cockpit.vue'
import CockpitComposer from '../components/cockpit/CockpitComposer.vue'
import CockpitLive from '../components/cockpit/CockpitLive.vue'
import ProjectPinned from '../components/cockpit/ProjectPinned.vue'
import MilestoneIssues from '../components/MilestoneIssues.vue'
import MilestoneNeeds from '../components/MilestoneNeeds.vue'
import MilestonePanel from '../components/MilestonePanel.vue'
import MilestoneRetro from '../components/MilestoneRetro.vue'
import MilestoneSections from '../components/MilestoneSections.vue'
import PlanList from '../components/PlanList.vue'
import ProjectFinishing from '../components/ProjectFinishing.vue'
import RouteTabs from '../ui/RouteTabs.vue'
import { live, useResource } from '../live'
import { needsOpen } from '../needsOpen'
import { pausePrompt } from '../pauseCard'
import {
  dayEndCursor,
} from '../stream'
import type {
  Cost,
  HistoryResponse,
  MilestoneView,
  ProjectDetail,
} from '../types'
import { milestoneStatus } from '../milestoneRows'
import { alertColor, toneText } from '../badgeTone'
import { pageHeaderUi } from '../pageHeader'
import { headerCost, keepCost, totalsCost } from '../headerCost'
import {
  milestoneTabFromRoute,
  milestoneTabs,
  milestoneTabsFor,
  panelFromQuery,
  panelQuery,
  panelViews,
  milestoneAlias,
  projectAlias,
  projectPanelViews,
  projectTabFromRoute,
  projectTabsFor,
  tabQuery,
  type PanelView,
} from '../streamTabs'
import NotFound from './NotFound.vue'
import { announceChange } from '../ui/useAnnounce'

// The queue and its drag library (vue-draggable-plus) load with the project
// page, not with the initial bundle (D662-P4).
const ProjectQueue = defineAsyncComponent(() => import('../components/ProjectQueue.vue'))

const props = defineProps<{
  namespace: string
  name: string
  iid?: string
  // problem: the problem route (the Cockpit with one thread open).
  problem?: string
}>()

const milestoneIID = computed(() => (props.iid ? Number(props.iid) : undefined))

// tab: Cockpit (the default), Milestones, Metrics or Plans (feature plans) on
// projects, Cockpit (the default), Issues, Metrics or Retrospective on
// milestones, kept in ?tab= so it can be linked to (a #prompt-Q<n> link
// without it opens the Cockpit). A milestone shows
// Retrospective only once it has one (milestoneTabsFor); a project's and a
// milestone's removed tabs' links open where their content moved
// (projectAlias, milestoneAlias). The milestone Cockpit tab's badge is the
// count of the Open items (D674-P2). The pilot's composer is at the bottom
// of the milestone's Cockpit; a project's Cockpit has none (D9).
const route = useRoute()
// pageMilestone: the loaded milestone, once it is the route's.
const pageMilestone = computed(() => (milestone.value?.iid === milestoneIID.value ? milestone.value : null))
const needsCount = computed(() => (pageMilestone.value ? needsOpen(pageMilestone.value).count : 0))
const tabs = computed(() => {
  if (!milestoneIID.value) return projectTabsFor(hasFeature('plans'))
  return milestoneTabsFor(pageMilestone.value)
})
// The header's Needs you count is the badge of the Cockpit tab: its link
// opens the Cockpit, whose top is the pinned cards it counts (R10, R14 of
// "Desk: the Cockpit").
const tabBadges = computed(() => (milestoneIID.value && needsCount.value ? { cockpit: needsCount.value } : undefined))
// A milestone's tab is read against all its tabs while it loads, so a
// ?tab=retrospective link waits for it; once loaded, a hidden tab falls
// back to the first one, the Cockpit (Decision 7).
const tab = computed((): string => {
  if (props.problem) return 'cockpit'
  if (milestoneIID.value) {
    const loaded = milestone.value?.iid === milestoneIID.value || milestoneFailed.value
    return milestoneTabFromRoute(route.query.tab, loaded ? tabs.value : milestoneTabs)
  }
  return projectTabFromRoute(route.query.tab, tabs.value)
})
// tabTo: a tab's link; from a problem's thread it leads back to the
// milestone's route.
function tabTo(id: string) {
  const query = tabQuery(route.query, id, tabs.value)
  if (!props.problem) return { query }
  return { name: 'stream-milestone' as const, params: { namespace: props.namespace, name: props.name, iid: props.iid }, query }
}
const router = useRouter()
const error = ref('')
const notFound = ref(false)
const project = ref<ProjectDetail | null>(null)

async function loadProject() {
  try {
    project.value = await getProject(props.namespace!, props.name!)
  } catch (e) {
    if (e instanceof NotFoundError) {
      notFound.value = true
      project.value = null
    } else if (!(e instanceof LoginRequiredError)) {
      error.value = errorMessage(e)
    }
  }
}

const { data: inbox } = useResource(() => getInbox, ['project', 'task'])

const history = ref<HistoryResponse | null>(null)
const milestone = ref<MilestoneView | null>(null)
// milestoneFailed: the scoped milestone could not be loaded (the Issues tab
// says so instead of loading forever).
const milestoneFailed = ref(false)

const projectInbox = computed(() => {
  const items = inbox.value?.items ?? []
  return items.filter((i) => i.namespace === props.namespace && i.project === props.name)
})

// The context panel (docs/contributing/web-ui.md, "Context panel and pause history"), laid out by
// ContextLayout: a column from 1180 px; below, a sheet that the header's
// buttons of the panel's views open. A project's is ContextPanel (Queue,
// Decisions; R15 of "Desk: the Cockpit"), a milestone's MilestonePanel
// (Issues, Queue, Decisions; R13); the view is ?panel=. Focus returns to
// the button that opened the sheet (panelOpener). A milestone's Issues tab
// (a table of fixed columns) needs the full width: there the panel is a
// sheet at every width.
const hasContext = computed(() => !!props.namespace && !!props.name)
const contextOpen = ref(false)
const panelOpener = ref<HTMLElement | null>(null)
const pagePanelViews = computed(() => (milestoneIID.value ? panelViews : projectPanelViews))
const panelView = computed(() => panelFromQuery(route.query.panel, pagePanelViews.value))
const panelLabel = computed(() => pagePanelViews.value.find((v) => v.id === panelView.value)!.label)
function setPanel(view: PanelView) {
  if (view === panelView.value) return
  panelBySwitch = true
  void router.replace({ query: panelQuery(route.query, view, pagePanelViews.value) })
}

// A day of the project panel's history pages the project's Cockpit back to
// the end of that day (D790-P2): the Cockpit tab opens, the sheet closes so
// the stream shows, and Back to now (in the panel and above the stream)
// returns to the newest events.
const cockpitDay = ref<string | undefined>()
const cockpitBefore = computed(() => (cockpitDay.value ? dayEndCursor(cockpitDay.value) : undefined))
const cockpitBeforeDay = computed(() =>
  cockpitDay.value ? new Date(`${cockpitDay.value}T12:00:00`).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : '',
)
function jumpDay(day: string) {
  cockpitDay.value = day
  contextOpen.value = false
  if (tab.value !== 'cockpit') void router.push({ query: tabQuery(route.query, 'cockpit', tabs.value) })
}

// The Milestones tab's views (R15): the milestone lists, or the Board
// (?view=board, also the old ?tab=board).
const milestonesViews = [
  { value: 'list', label: 'Milestones' },
  { value: 'board', label: 'Board' },
]
const milestonesView = computed(() => (route.query.view === 'board' ? 'board' : 'list'))
function setMilestonesView(v: string | number) {
  const { view: _, ...rest } = route.query
  void router.replace({ query: v === 'board' ? { ...rest, view: 'board' } : rest })
}
function openContext(e: MouseEvent, view?: PanelView) {
  panelOpener.value = e.currentTarget as HTMLElement
  if (view) setPanel(view)
  contextOpen.value = true
}

// A link that names a panel view (?panel=, also the aliased
// ?tab=decisions, a decision link or the tracking issue's) opens the sheet
// where the panel is one, so the view shows; the view switch inside the
// sheet (setPanel) and closing the sheet do not reopen it.
const panelColumn = computed(() => !milestoneIID.value || tab.value !== 'issues')
let panelBySwitch = false
watch(
  () => [milestoneIID.value, route.query.panel] as const,
  ([, panel]) => {
    if (panelBySwitch) return void (panelBySwitch = false)
    if (!panel || route.query.tab === 'decisions') return
    if (!panelColumn.value || window.matchMedia('(max-width: 1179.98px)').matches) {
      panelOpener.value = null
      contextOpen.value = true
    }
  },
  { immediate: true },
)
const canSteer = computed(() => !!milestone.value?.permissions?.steer)

// paused: the loaded milestone's paused autopilot, pinned above the composer
// (project and milestone pages).
const paused = computed(() => {
  const m = milestone.value
  return m?.autopilot?.phase === 'Paused' ? m : null
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
    to: { name: 'stream-milestone', params: { namespace: props.namespace, name: props.name, iid: String(m.iid) } },
  }
})

async function loadHistory() {
  try {
    history.value = await getProjectHistory(props.namespace!, props.name!, 30, Intl.DateTimeFormat().resolvedOptions().timeZone)
  } catch (e) {
    if (e instanceof NotFoundError) history.value = null
  }
}

async function loadMilestone() {
  const iid = milestoneIID.value
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

// pageProject: the loaded project, once it is the route's (the old one stays
// in project while the next loads).
const pageProject = computed(() => {
  const p = project.value
  return p && p.namespace === props.namespace && p.name === props.name ? p : null
})

// projectTasks: the implement tasks of all the project's milestones, for the
// project Cockpit's live lines, and each task's milestone, whose Cockpit
// its line links to (R15).
const projectTasks = computed(() => {
  const ms = pageProject.value?.milestones ?? []
  return {
    tasks: ms.flatMap((m) => m.tasks),
    milestones: Object.fromEntries(ms.flatMap((m) => m.tasks.map((t) => [t.name, m.iid]))),
  }
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

// An old link to a removed milestone tab (?tab=activity, needs, pilot,
// decisions, board) or to the pilot drawer (?pilot=1) opens where its
// content moved (milestoneAlias, R12 and R14), and so does an old link to a
// removed project tab (?tab=activity, decisions, board; projectAlias, R15):
// the URL is replaced on load.
watch(
  () => [milestoneIID.value, route.query] as const,
  ([iid, query]) => {
    const next = iid ? milestoneAlias(query) : projectAlias(query)
    if (next) void router.replace({ query: next, hash: route.hash })
  },
  { immediate: true },
)

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
  () => (!pageProject.value ? null : headerCost(pageProject.value, pageMilestone.value, milestoneIID.value)),
  showCost,
  { immediate: true },
)
const pageCost = computed(() => (shownCost.value.key === costKey.value ? shownCost.value.cost : null))

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
    cockpitDay.value = undefined
    void loadProject()
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
    :column="panelColumn"
    :title="panelLabel"
    :description="milestoneIID ? 'The milestone\'s issues, the project\'s queue and the milestone\'s decisions' : 'The queue and companions of the project\'s active milestone, its last 30 days, and its decisions'"
    :opener="panelOpener"
  >
    <template #default="{ sheet }">
    <div class="stream">
      <!-- The page header (decision 4): the milestone's status as eyebrow,
           the title, the cost as meta line (one number on every tab,
           headerCost.ts), the panel's view buttons (below 1180 px) and ⋯ with the links to
           GitLab on the right, then the tab row; the page's alerts are in
           the tabs. Filters and search are in the tabs'
           toolbars (decision 3). -->
      <div data-stream-head class="bg-(--bg) px-4 pt-3.5 tablet:px-7">
        <UPageHeader :title="title" :ui="pageHeaderUi">
          <template v-if="status" #headline>
            <span :class="toneText[status.tone]" data-stream-status>{{ status.label }}</span>
          </template>
          <template v-if="pageCost" #description><CostChip :cost="pageCost" data-header-cost /></template>
          <template #links>
            <HeaderActions :actions="links" :shown="0">
              <!-- The panel's views (below 1180 px, phones too). -->
              <template v-if="sheet">
                <UButton
                  v-for="v in pagePanelViews"
                  :key="v.id"
                  color="neutral"
                  variant="outline"
                  size="sm"
                  class="min-h-11 justify-center tablet:min-h-0"
                  :label="v.label"
                  aria-haspopup="dialog"
                  :aria-expanded="contextOpen && panelView === v.id"
                  :data-panel-open="v.id"
                  @click="openContext($event, v.id)"
                />
              </template>
            </HeaderActions>
          </template>
        </UPageHeader>
        <RouteTabs :tabs="tabs" :active="tab" :to="tabTo" :badges="tabBadges" />
      </div>

      <!-- A project's milestones (R9), in the dashboard's lists, or its
           Board (?view=board, R15), picked in the tab's toolbar. -->
      <div v-if="tab === 'milestones'" class="scroll board">
        <UDashboardToolbar class="mb-3 min-h-0 border-0 px-0 sm:px-0" data-tab-toolbar>
          <template #left>
            <UTabs
              :model-value="milestonesView"
              :items="milestonesViews"
              variant="pill"
              size="sm"
              :content="false"
              aria-label="View"
              data-milestones-view-switch
              @update:model-value="setMilestonesView"
            />
          </template>
        </UDashboardToolbar>
        <template v-if="milestonesView === 'board'">
          <ProjectBoards v-if="namespace && name && project" :namespace="namespace" :name="name" :project="project" />
          <UAlert v-else-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
          <div v-else class="empty">Loading…</div>
        </template>
        <div v-else-if="namespace && name" class="milestones">
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
      <div v-else-if="tab === 'cockpit' && !milestoneIID && namespace && name" class="scroll board">
        <!-- The project's Cockpit (R15): the pinned cards of every milestone
             with open needs under its name, the stream across the
             milestones folded per milestone and issue, the live lines of
             all running issues under the pinned cards, each a link to its
             milestone's Cockpit; no composer (D9). -->
        <UAlert
          v-if="cockpitBefore"
          :color="alertColor('info')"
          variant="subtle"
          class="mb-3"
          :description="`Events up to the end of ${cockpitBeforeDay}.`"
          :actions="[{ label: 'Back to now', color: 'neutral', variant: 'outline', onClick: () => (cockpitDay = undefined) }]"
          data-cockpit-jumped
        />
        <Cockpit :scope="{ namespace, name, before: cockpitBefore }" :project="project" :can-steer="canSteer">
          <template #pinned>
            <ProjectPinned :namespace="namespace" :name="name" :project="pageProject" :inbox="projectInbox" />
          </template>
          <template #live>
            <CockpitLive :namespace="namespace" :name="name" :tasks="projectTasks.tasks" :milestones="projectTasks.milestones" />
          </template>
        </Cockpit>
      </div>
      <div v-else-if="tab === 'cockpit'" class="scroll board">
        <!-- The milestone's Cockpit (R10): its problem cards, proposals
             and prompts pinned, answered in place, then its stream. -->
        <Cockpit :scope="{ namespace, name, milestoneIID }" :project="project" :prompts="pageMilestone?.prompts" :can-steer="canSteer">
          <template #pinned>
            <MilestoneNeeds
              v-if="namespace && name && pageMilestone"
              :namespace="namespace"
              :name="name"
              :milestone="pageMilestone"
              :problem="problem"
            />
            <UAlert v-else-if="milestoneFailed" :color="alertColor('danger')" variant="subtle" class="mt-3" description="The milestone could not be loaded." />
            <div v-else class="empty">Loading…</div>
          </template>
          <!-- The composer to the pilot (R12) on top; a stored milestone has
               none. -->
          <template #composer="{ sent, reload }">
            <CockpitComposer
              v-if="namespace && name && pageMilestone && !pageMilestone.stored"
              :namespace="namespace"
              :name="name"
              :milestone="pageMilestone"
              :project="project"
              @sent="sent"
              @posted="reload"
            />
          </template>
          <!-- The live lines of running issues (R11), under the pinned cards. -->
          <template #live>
            <CockpitLive
              v-if="namespace && name && pageMilestone"
              :namespace="namespace"
              :name="name"
              :tasks="pageMilestone.tasks"
              :transcript="!!pageMilestone.permissions?.transcript"
            />
          </template>
        </Cockpit>
      </div>
      <div v-else-if="tab === 'issues'" class="scroll board">
        <!-- The full pause card is pinned in the Cockpit. -->
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

    </div>
    </template>

    <!-- A project's or a milestone's context: a column from 1180 px, a
         sheet below. -->
    <template v-if="hasContext" #panel="{ sheet }">
      <MilestonePanel
        v-if="milestoneIID"
        :namespace="namespace!"
        :name="name!"
        :iid="milestoneIID"
        :milestone="pageMilestone"
        :queue="pageProject?.queue ?? []"
        :view="panelView"
        :sheet="sheet"
        @update:view="setPanel"
      />
      <ContextPanel
        v-else
        :class="{ 'ctx-sheet': sheet }"
        :namespace="namespace!"
        :name="name!"
        :view="panelView"
        :milestone="milestone"
        :milestones="project?.milestones"
        :history="history"
        :jumped="cockpitBefore"
        @update:view="setPanel"
        @day="jumpDay"
        @clear="cockpitDay = undefined"
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
.scroll {
  overflow-y: auto;
  flex: 1;
  padding: 0 28px 40px;
}
.scroll.board {
  padding-top: 18px;
}
.milestones {
  padding-top: 4px;
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
}
</style>
