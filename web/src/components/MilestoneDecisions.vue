<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import Badge from './Badge.vue'
import DecisionCard from './DecisionCard.vue'
import { getDecisions } from '../client'
import {
  decisionFromHash,
  decisionsQuery,
  decisionStates,
  filtered,
  groupByIssue,
  issueStateLabel,
  milestoneLabel,
  searchFromQuery,
  stateFromQuery,
  type DecisionGroup,
} from '../decisions'
import { useResource } from '../live'
import { searchToolbarUi } from '../pageHeader'

// The Decisions tab (R11): the stored decisions of a milestone (milestone 0:
// the whole project, for the project tab) grouped by issue in the server's
// order, one DecisionCard each. The search (?q=, debounced) and the
// All / Active / Superseded pills (?state=) live in the URL, so they survive
// a reload; the server filters. The controls slot takes more filters (the
// project tab's milestone select). A #<id> link (decisionHref, the old
// ?panel=decisions#<id>) scrolls to its card once the data is there, moves
// focus to it and highlights it briefly; so does a later hash change. All,
// the default state, lists the superseded cards too, so no group needs
// opening first; under Active, a link to a card it hides switches to All.
// Across all milestones (milestone 0), each group's header names its
// milestone, by its title in milestones (the project's list).
const props = defineProps<{
  namespace: string
  name: string
  milestone: number
  milestones?: readonly { iid: number; title: string }[]
}>()

const route = useRoute()
const router = useRouter()
const q = computed(() => searchFromQuery(route.query.q))
const state = computed(() => stateFromQuery(route.query.state))

// Stored decisions change when the operator reads the issues: on task events.
const { data, error, loading } = useResource(() => {
  const { namespace, name, milestone } = props
  const params = { milestone, q: q.value, state: state.value }
  return () => getDecisions(namespace, name, params)
}, ['task'])

const groups = computed(() => groupByIssue(data.value?.decisions ?? []))
const byID = computed(() => new Map((data.value?.decisions ?? []).map((d) => [d.id, d])))
const emptyText = computed(() =>
  filtered({ q: q.value, state: state.value }) ? 'No decisions match.' : 'No decisions recorded yet.',
)

// The search box writes ?q= about 300 ms after the last key.
const draft = ref(q.value)
let typing: ReturnType<typeof setTimeout> | undefined
watch(q, (v) => {
  if (v !== draft.value.trim()) draft.value = v
})
watch(draft, (v) => {
  clearTimeout(typing)
  typing = setTimeout(() => {
    if (v.trim() !== q.value) void router.replace({ query: decisionsQuery(route.query, { q: v }) })
  }, 300)
})
const stateItems = decisionStates.map((s) => ({ value: s.id, label: s.label }))
function setState(v: string | number) {
  const s = decisionStates.find((d) => d.id === v)?.id
  if (!s) return
  void router.replace({ query: decisionsQuery(route.query, { state: s }) })
}

function milestoneTo(g: DecisionGroup) {
  const { namespace, name } = props
  return { name: 'stream-milestone' as const, params: { namespace, name, iid: String(g.milestoneIID) }, query: { tab: 'decisions' } }
}

function issueTo(g: DecisionGroup) {
  const { namespace, name } = props
  return { name: 'issue' as const, params: { namespace, name, iid: String(g.milestoneIID), issue: String(g.issue.iid) } }
}

// linked is the card a #<id> link opened, highlighted for two seconds.
// followed keeps a reload (on events) from scrolling to it again.
const linked = ref('')
let followed = ''
let flash: ReturnType<typeof setTimeout> | undefined
function follow() {
  const id = decisionFromHash(route.hash)
  if (!id || id === followed || !data.value) return
  if (!data.value.decisions.some((d) => d.id === id)) {
    // Active hides superseded cards: a link to one switches to All.
    if (state.value === 'active') void router.replace({ query: decisionsQuery(route.query, { state: 'all' }), hash: route.hash })
    return
  }
  followed = id
  void nextTick(() => {
    const card = document.getElementById(id)
    card?.scrollIntoView({ block: 'start' })
    card?.focus({ preventScroll: true })
    linked.value = id
    clearTimeout(flash)
    flash = setTimeout(() => (linked.value = ''), 2000)
  })
}
watch(
  () => route.hash,
  () => {
    followed = ''
    follow()
  },
)
watch(data, follow)

onBeforeUnmount(() => {
  clearTimeout(typing)
  clearTimeout(flash)
})

// An issue's group is a section (h2) with the look it had as an h3.
const sectionHead =
  'm-0 flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1 text-sm font-semibold tracking-[0.04em] text-(--text-2) uppercase'
</script>

<template>
  <section data-decisions>
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <UAlert v-else-if="data && !data.available" :color="alertColor('info')" variant="subtle" class="mt-3" description="Decisions need the history database." />
    <template v-else>
      <!-- The tab's toolbar (decision 3): the state pills (?state=) and the
           controls slot left, the search right, on its own row on phones. -->
      <UDashboardToolbar :ui="searchToolbarUi" data-tab-toolbar>
        <template #left>
          <UTabs
            :model-value="state"
            :items="stateItems"
            variant="pill"
            size="sm"
            :content="false"
            aria-label="State"
            data-decision-state
            @update:model-value="setState"
          />
          <slot name="controls" />
        </template>
        <template #right>
          <form role="search" class="w-full tablet:w-60" @submit.prevent>
            <UInput
              v-model="draft"
              type="search"
              placeholder="Search decisions…"
              aria-label="Search decisions"
              icon="i-lucide-search"
              class="w-full"
              :ui="{ base: 'tablet:text-sm/5' }"
            />
          </form>
        </template>
      </UDashboardToolbar>

      <div v-if="loading && !data" class="empty">Loading…</div>
      <UEmpty v-else-if="!groups.length" icon="i-lucide-scale" :title="emptyText" variant="naked" size="sm" />
      <template v-else>
        <div v-for="g in groups" :key="g.key" class="mt-[18px] flex min-w-0 flex-col gap-2.5" data-decision-group>
          <h2 :class="sectionHead">
            <RouterLink
              v-if="!milestone"
              :to="milestoneTo(g)"
              class="inline-flex min-h-11 min-w-0 items-center font-medium [overflow-wrap:anywhere] text-(--text-3) tablet:min-h-0"
              data-decision-milestone
            >
              {{ milestoneLabel(g.milestoneIID, milestones) }}
            </RouterLink>
            <span v-if="!milestone" aria-hidden="true" class="text-(--text-3)">/</span>
            <RouterLink :to="issueTo(g)" class="inline-flex min-h-11 min-w-0 items-center [overflow-wrap:anywhere] tablet:min-h-0">
              {{ g.issue.title || `#${g.issue.iid}` }}
            </RouterLink>
            <span class="font-medium whitespace-nowrap tabular-nums text-(--text-3)">#{{ g.issue.iid }}</span>
            <Badge class="self-center" :tone="g.issue.state === 'opened' ? 'progress' : 'muted'">
              {{ issueStateLabel(g.issue.state) }}
            </Badge>
          </h2>
          <DecisionCard
            v-for="d in g.decisions"
            :key="d.id"
            :namespace="namespace"
            :name="name"
            :decision="d"
            :byID="byID"
            :linked="linked === d.id"
          />
        </div>
      </template>
    </template>
  </section>
</template>
