<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { problemAnchor, type Problem } from '../needsCases'
import Badge from './Badge.vue'
import NeedsEntries from './NeedsEntries.vue'

// NeedsProblem is one problem of the Needs you tab's Handled section (R14):
// an issue, or the milestone (iid 0), with its prompts, the pilot's actions
// and its pauses, newest first (NeedsEntries). Open problems and the one the
// route hash names (#problem-<iid>, or #prompt-<id> of one of its prompts)
// start unfolded; the chevron button folds the entries. The heading is no
// <summary>: a summary's children are presentational, so the issue link
// would not be a link there. inOpen: the problem has a card under Open,
// which carries the #problem-<iid> anchor.
//
// Utilities only (docs/contributing/web-ui.md, "Needs you tab"): on phones
// the chevron is 44 px high.
const props = defineProps<{
  namespace: string
  name: string
  milestone: number
  problem: Problem
  linked: boolean
  inOpen?: boolean
}>()

const route = useRoute()
const anchor = computed(() => problemAnchor(props.problem.iid))
const issueTo = computed(() => ({
  name: 'issue' as const,
  params: { namespace: props.namespace, name: props.name, iid: String(props.milestone), issue: String(props.problem.iid) },
}))

// unfolded: the problem's fold; it follows the user's clicks and opens when
// the hash names the problem or one of its prompts.
const unfolded = ref(props.problem.open || props.linked)
const el = ref<HTMLElement | null>(null)
// heading: "#N title". Built in script: a leading space inside a
// <template> is condensed away, which glued the title to the number.
const heading = computed(() => [`#${props.problem.iid}`, props.problem.title].filter(Boolean).join(' '))
const what = computed(() => (props.problem.iid ? `#${props.problem.iid}` : 'the milestone'))

function follow() {
  if (props.linked) unfolded.value = true
  if (!props.inOpen && route.hash === `#${anchor.value}`) void nextTick(() => el.value?.scrollIntoView({ block: 'start' }))
}
follow()
watch(() => [route.hash, props.linked], follow)
// A problem that turns open while the page is live (a new prompt, a hold)
// unfolds, so its open row is in sight.
watch(
  () => props.problem.open,
  (open) => {
    if (open) unfolded.value = true
  },
)

const quiet = 'text-[12.5px] text-(--text-3)'
const chevron = 'inline-block text-(--text-3) transition-transform motion-reduce:transition-none'
</script>

<template>
  <section
    :id="inOpen ? undefined : anchor"
    ref="el"
    class="card min-w-0 scroll-mt-3 px-3.5 py-2.5 text-[13.5px]"
    :data-problem="problem.iid"
    :data-open="unfolded ? '' : undefined"
  >
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1" data-problem-head>
      <button
        type="button"
        class="-ms-1.5 inline-flex size-11 cursor-pointer items-center justify-center rounded-(--radius-sm) tablet:size-6"
        :aria-expanded="unfolded"
        :aria-controls="`${anchor}-entries`"
        :aria-label="`Entries of ${what}`"
        data-problem-toggle
        @click="unfolded = !unfolded"
      >
        <span aria-hidden="true" :class="[chevron, unfolded && 'rotate-90']">▸</span>
      </button>
      <RouterLink v-if="problem.iid" :to="issueTo" class="min-w-0 font-semibold wrap-anywhere">
        {{ heading }}
      </RouterLink>
      <b v-else>Milestone</b>
      <Badge :tone="problem.open ? 'warning' : 'neutral'">{{ problem.open ? 'Open' : 'Handled' }}</Badge>
      <span v-if="problem.attempts" class="ms-auto" :class="quiet" data-attempts>
        Pilot attempts: {{ problem.attempts.used }} of {{ problem.attempts.max }}
      </span>
    </div>

    <NeedsEntries
      v-show="unfolded"
      :id="`${anchor}-entries`"
      class="mt-2.5"
      :namespace="namespace"
      :name="name"
      :entries="problem.entries"
      anchored
    />
  </section>
</template>
