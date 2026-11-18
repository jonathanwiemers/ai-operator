<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import UInput from '@nuxt/ui/components/Input.vue'
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
  searchFromQuery,
  stateFromQuery,
  type DecisionGroup,
  type DecisionState,
} from '../decisions'
import { useResource } from '../live'

// The Decisions tab (R11): the stored decisions of a milestone (milestone 0:
// the whole project, for the project tab) grouped by issue in the server's
// order, one DecisionCard each. The search (?q=, debounced) and the
// All / Active / Superseded chips (?state=) live in the URL, so they survive
// a reload; the server filters. The controls slot takes more filters (the
// project tab's milestone select). A #<id> link scrolls to its card once
// the data is there and highlights it briefly; so does a later hash change.
const props = defineProps<{ namespace: string; name: string; milestone: number }>()

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
function setState(s: DecisionState) {
  void router.replace({ query: decisionsQuery(route.query, { state: s }) })
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
  if (!id || id === followed || !data.value?.decisions.some((d) => d.id === id)) return
  followed = id
  void nextTick(() => {
    document.getElementById(id)?.scrollIntoView({ block: 'start' })
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

const sectionHead = 'm-0 flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1 text-sm font-semibold'
</script>

<template>
  <section data-decisions>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-else-if="data && !data.available" class="banner info">Decisions need the history database.</div>
    <template v-else>
      <div class="flex flex-wrap items-center gap-2">
        <form role="search" class="basis-full tablet:basis-auto" @submit.prevent>
          <UInput
            v-model="draft"
            type="search"
            placeholder="Search decisions…"
            aria-label="Search decisions"
            icon="i-lucide-search"
            class="w-full tablet:w-60"
            :ui="{ base: 'tablet:text-sm/5' }"
          />
        </form>
        <div class="flex flex-wrap gap-1.5" role="group" aria-label="State">
          <button
            v-for="s in decisionStates"
            :key="s.id"
            type="button"
            class="min-h-11 cursor-pointer rounded-full border px-2.5 text-[12.5px] font-[inherit] tablet:min-h-0 tablet:py-[3px]"
            :class="
              state === s.id
                ? 'border-(--text) bg-(--text) text-(--bg)'
                : 'border-(--border) bg-(--surface) text-(--text-2)'
            "
            :aria-pressed="state === s.id"
            @click="setState(s.id)"
          >
            {{ s.label }}
          </button>
        </div>
        <slot name="controls" />
      </div>

      <div v-if="loading && !data" class="empty">Loading…</div>
      <div v-else-if="!groups.length" class="empty">{{ emptyText }}</div>
      <template v-else>
        <div v-for="g in groups" :key="g.key" class="mt-[18px] flex min-w-0 flex-col gap-2.5" data-decision-group>
          <h3 :class="sectionHead">
            <RouterLink :to="issueTo(g)" class="inline-flex min-h-11 min-w-0 items-center [overflow-wrap:anywhere] tablet:min-h-0">
              {{ g.issue.title || `#${g.issue.iid}` }}
            </RouterLink>
            <span class="font-medium whitespace-nowrap tabular-nums text-(--text-3)">#{{ g.issue.iid }}</span>
            <Badge class="self-center" :tone="g.issue.state === 'opened' ? 'progress' : 'muted'">
              {{ issueStateLabel(g.issue.state) }}
            </Badge>
          </h3>
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
