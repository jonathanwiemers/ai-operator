<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UCollapsible from '@nuxt/ui/components/Collapsible.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { alertColor } from '../badgeTone'
import { getDecisions } from '../client'
import { useResource } from '../live'
import { panelIssueGroups } from '../milestoneRows'
import { queueStateLabel, queueTone } from '../projectQueue'
import { milestoneTabs, panelViews, tabQuery, type PanelView } from '../streamTabs'
import type { QueueEntry } from '../generated/views'
import type { MilestoneView } from '../types'
import Badge from './Badge.vue'

// MilestonePanel is a milestone page's context panel (R13 of "Desk: the
// Cockpit"), laid out by ContextLayout: a column from 1180 px, a sheet below
// that the header's Issues, Queue and Decisions buttons open. A switch at
// the top picks the view, which Stream.vue keeps in ?panel=:
//   - Issues: the milestone's issues by state (panelIssueGroups), each a
//     link to its issue page, and Open table to the Issues tab;
//   - Queue: the project's milestone queue (ProjectQueue's data), this
//     milestone marked;
//   - Decisions: the milestone's stored decisions, the superseded ones in a
//     closed group. They load while the view is open.
const props = defineProps<{
  namespace: string
  name: string
  iid: number
  milestone: MilestoneView | null
  queue: QueueEntry[]
  view: PanelView
  // sheet: the panel is in the slide-over, which has its own edge and scroll.
  sheet?: boolean
}>()
const emit = defineEmits<{ 'update:view': [view: PanelView] }>()

const route = useRoute()
const viewItems = panelViews.map((v) => ({ value: v.id, label: v.label }))
function setView(v: string | number) {
  const view = panelViews.find((p) => p.id === v)?.id
  if (view) emit('update:view', view)
}

const groups = computed(() => (props.milestone ? panelIssueGroups(props.milestone.issues, props.milestone.autopilot?.items ?? []) : []))
const tableTo = computed(() => ({ query: tabQuery(route.query, 'issues', milestoneTabs) }))
function issueTo(iid: number) {
  const { namespace, name } = props
  return { name: 'issue' as const, params: { namespace, name, iid: String(props.iid), issue: String(iid) } }
}
function milestoneTo(iid: number) {
  const { namespace, name } = props
  return { name: 'stream-milestone' as const, params: { namespace, name, iid: String(iid) } }
}

const decisions = useResource(() => {
  if (props.view !== 'decisions') return null
  const { namespace, name, iid } = props
  return () => getDecisions(namespace, name, { milestone: iid, q: '', state: 'all' })
}, ['task'])
const active = computed(() => decisions.data.value?.decisions.filter((d) => !d.superseded) ?? [])
const superseded = computed(() => decisions.data.value?.decisions.filter((d) => d.superseded) ?? [])

// A decision link (#<id>, decisionHref; the old ?tab=decisions#<id>) lands
// on its row once the decisions have loaded; a superseded one opens its
// closed group first.
const supersededOpen = ref(false)
watch(
  () => [decisions.data.value, route.hash] as const,
  async ([, hash]) => {
    const id = hash.slice(1)
    if (!id) return
    const old = superseded.value.some((d) => d.id === id)
    if (!old && !active.value.some((d) => d.id === id)) return
    if (old) supersededOpen.value = true
    await nextTick()
    await new Promise((r) => requestAnimationFrame(r))
    document.getElementById(id)?.scrollIntoView({ block: 'nearest' })
  },
  { immediate: true },
)

</script>

<template>
  <aside class="mpanel" :class="{ column: !sheet }" aria-label="Milestone context" data-milestone-panel>
    <UTabs
      :model-value="view"
      :items="viewItems"
      variant="pill"
      size="sm"
      :content="false"
      aria-label="Panel view"
      data-panel-switch
      @update:model-value="setView"
    />

    <div v-if="view === 'issues'" class="view" data-panel-view="issues">
      <div v-if="!milestone" class="faint">Loading…</div>
      <UEmpty v-else-if="!groups.length" icon="i-lucide-list" title="No issues yet." variant="naked" size="xs" />
      <section v-for="g in groups" :key="g.state" :data-panel-state="g.state">
        <h2>
          <span>{{ g.label }}</span><span>{{ g.issues.length }}</span>
        </h2>
        <ul>
          <li v-for="i in g.issues" :key="i.iid">
            <RouterLink :to="issueTo(i.iid)" class="row">
              <span class="ref">#{{ i.iid }}</span>
              <span>{{ i.title }}</span>
            </RouterLink>
          </li>
        </ul>
      </section>
      <UButton :to="tableTo" color="neutral" variant="outline" size="sm" icon="i-lucide-table" label="Open table" class="table" data-panel-open-table />
    </div>

    <div v-else-if="view === 'queue'" class="view" data-panel-view="queue">
      <UEmpty v-if="!queue.length" icon="i-lucide-inbox" title="No milestones in the queue." variant="naked" size="xs" />
      <ol v-else>
        <li v-for="(e, n) in queue" :key="e.iid" :data-panel-queue-entry="e.iid">
          <RouterLink :to="milestoneTo(e.iid)" class="row" :class="{ current: e.iid === iid }" :aria-current="e.iid === iid ? 'page' : undefined">
            <span class="ref">{{ n + 1 }}</span>
            <span class="title"><span class="ref">%{{ e.iid }}</span> {{ e.title }}</span>
            <Badge :tone="queueTone(e)">{{ queueStateLabel(e) }}</Badge>
            <span v-if="e.iid === iid" class="faint" data-panel-queue-this>This milestone</span>
          </RouterLink>
        </li>
      </ol>
    </div>

    <div v-else class="view" data-panel-view="decisions">
      <UAlert v-if="decisions.error.value" :color="alertColor('danger')" variant="subtle" :description="decisions.error.value" />
      <UAlert
        v-else-if="decisions.data.value && !decisions.data.value.available"
        :color="alertColor('info')"
        variant="subtle"
        description="Decisions need the history database."
      />
      <div v-else-if="!decisions.data.value" class="faint">Loading…</div>
      <UEmpty v-else-if="!active.length && !superseded.length" icon="i-lucide-scale" title="No decisions recorded yet." variant="naked" size="xs" />
      <template v-else>
        <ul>
          <li v-for="d in active" :id="d.id" :key="d.id" class="scroll-mt-3" data-panel-decision>
            <RouterLink :to="issueTo(d.issue.iid)" class="row">
              <span class="ref">{{ d.id }}</span>
              <span>{{ d.title }}</span>
            </RouterLink>
            <p class="choice">{{ d.choice }}</p>
          </li>
        </ul>
        <UCollapsible v-if="superseded.length" v-model:open="supersededOpen" data-panel-superseded>
          <template #default="{ open }">
            <UButton
              color="neutral"
              variant="ghost"
              size="sm"
              class="min-h-11 tablet:min-h-0"
              :label="`Superseded (${superseded.length})`"
              :trailing-icon="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
            />
          </template>
          <template #content>
            <ul>
              <li v-for="d in superseded" :id="d.id" :key="d.id" class="scroll-mt-3">
                <RouterLink :to="issueTo(d.issue.iid)" class="row old">
                  <span class="ref">{{ d.id }}</span>
                  <span>{{ d.title }}</span>
                </RouterLink>
              </li>
            </ul>
          </template>
        </UCollapsible>
      </template>
    </div>
  </aside>
</template>

<!-- Scoped, not Tailwind utilities: they load with the page, not with the
     initial stylesheet (docs/contributing/web-ui.md, "Bundle budget"). -->
<style scoped>
.mpanel {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  align-content: start;
  gap: 14px;
  min-width: 0;
  min-height: 0;
  padding: 16px;
  background: var(--surface);
}
.mpanel.column {
  overflow-y: auto;
  border-left: 1px solid var(--border);
}
.view {
  display: grid;
  gap: 12px;
  min-width: 0;
}
h2 {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  margin: 0 0 6px;
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-3);
}
ul,
ol {
  display: grid;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 6px;
  min-width: 0;
  min-height: 44px;
  padding: 4px 6px;
  border-radius: 6px;
  font-size: 13px;
  color: var(--text);
  overflow-wrap: anywhere;
}
.row:hover {
  background: var(--surface-2);
  text-decoration: none;
}
.row.current {
  background: var(--surface-2);
  font-weight: 600;
}
.row.old {
  color: var(--text-2);
}
.ref {
  flex-shrink: 0;
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 12px;
  color: var(--text-3);
}
.title {
  flex: 1 1 8rem;
  min-width: 0;
}
.faint {
  font-size: 12px;
  font-weight: 400;
  color: var(--text-3);
}
.choice {
  margin: 0;
  padding-left: 6px;
  font-size: 12px;
  color: var(--text-2);
  overflow-wrap: anywhere;
}
.table {
  justify-self: start;
  min-height: 44px;
}
@media (min-width: 760px) {
  .row,
  .table {
    min-height: 0;
  }
}
</style>
