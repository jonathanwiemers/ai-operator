<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { problemChatRef } from '../chatState'
import { warmTask } from '../client'
import { problemAnchor } from '../needsCases'
import type { OpenProblem } from '../needsOpen'
import type { MilestoneView } from '../types'
import { hidesReason, problemCause } from '../problemFacts'
import AskPilot from './AskPilot.vue'
import NeedsEntries from './NeedsEntries.vue'
import PauseCard from './PauseCard.vue'
import ProblemFacts from './ProblemFacts.vue'
import PromptCard from './PromptCard.vue'

// ProblemCard is one problem under Open on the Needs you tab (Decision 1):
// an issue, or the milestone (iid 0), with the milestone's pause when it
// belongs to it, the issue's hold (both PauseCard, which shows its own
// prompt), its other open prompts and its history folded (NeedsEntries,
// without anchors: Handled has them). The card carries #problem-<iid>.
// First come the cause line and the facts strip (R8, problemFacts.ts): the
// cause replaces the generic reason of a needs-input pause or hold.
// Under the options is the pilot's chat (R9): the Pilot tab's thread, this
// problem's messages first; its composer is the free-text box, and what it
// sends starts with "problem: #N". A mounted card wakes the pilot (warm).
//
// Utilities only (docs/contributing/web-ui.md, "Needs you tab"): on phones
// the history's fold is 44 px high.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView; problem: OpenProblem }>()

const anchor = computed(() => problemAnchor(props.problem.iid))
const issueTo = computed(() => ({
  name: 'issue' as const,
  params: { namespace: props.namespace, name: props.name, iid: String(props.milestone.iid), issue: String(props.problem.iid) },
}))
// heading: "#N title", built in script (see NeedsProblem).
const heading = computed(() => [`#${props.problem.iid}`, props.problem.title].filter(Boolean).join(' '))

const facts = computed(() => props.milestone.problems?.find((f) => f.iid === props.problem.iid))
const cause = computed(() => problemCause(facts.value))
const chatRef = computed(() => problemChatRef(props.problem.iid))
const chats = computed(() => !!props.milestone.pilot && !!props.milestone.permissions?.chat)
onMounted(() => {
  if (chats.value) void warmTask(props.namespace, props.name, props.milestone.pilot!.task)
})

const route = useRoute()
const el = ref<HTMLElement | null>(null)
function scrollIfLinked() {
  if (route.hash === `#${anchor.value}`) void nextTick(() => el.value?.scrollIntoView({ block: 'start' }))
}
onMounted(scrollIfLinked)
watch(() => route.hash, scrollIfLinked)

const quiet = 'text-[12.5px] text-(--text-3)'
const chevron = 'inline-block text-(--text-3) transition-transform motion-reduce:transition-none'
</script>

<template>
  <section
    :id="anchor"
    ref="el"
    class="card grid min-w-0 scroll-mt-3 gap-2.5 px-2.5 py-2.5 text-[13.5px] tablet:px-3.5"
    :data-open-problem="problem.iid"
  >
    <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1" data-problem-head>
      <RouterLink v-if="problem.iid" :to="issueTo" class="min-w-0 font-semibold wrap-anywhere">{{ heading }}</RouterLink>
      <b v-else>Milestone</b>
      <span v-if="problem.attempts" class="ms-auto" :class="quiet" data-attempts>
        Pilot attempts: {{ problem.attempts.used }} of {{ problem.attempts.max }}
      </span>
    </div>
    <p v-if="cause" class="m-0 min-w-0 font-medium wrap-anywhere" data-problem-cause>{{ cause }}</p>
    <ProblemFacts v-if="facts" :namespace="namespace" :name="name" :facts="facts" />
    <PauseCard v-if="problem.paused" :namespace="namespace" :name="name" :milestone="milestone" :no-reason="hidesReason(cause, milestone.autopilot?.pauseKind)" :facts="facts" />
    <PauseCard v-if="problem.hold" :namespace="namespace" :name="name" :milestone="milestone" :hold="problem.hold" :no-reason="hidesReason(cause, problem.hold.kind)" :facts="facts" />
    <PromptCard v-for="p in problem.prompts" :key="p.id" :namespace="namespace" :name="name" :prompt="p" :facts="facts" />
    <div v-if="chats" class="grid min-w-0 gap-1.5" data-problem-chat>
      <span :class="quiet">Pilot chat</span>
      <AskPilot :namespace="namespace" :name="name" :milestone="milestone" :focus="chatRef" :message-prefix="chatRef" />
    </div>
    <details v-if="problem.entries.length" class="group/history min-w-0" data-problem-history>
      <summary class="flex min-h-11 cursor-pointer items-center gap-2 tablet:min-h-0" :class="quiet">
        <span aria-hidden="true" :class="[chevron, 'group-open/history:rotate-90']">▸</span>
        History ({{ problem.entries.length }})
      </summary>
      <NeedsEntries class="mt-1.5" :namespace="namespace" :name="name" :entries="problem.entries" />
    </details>
  </section>
</template>
