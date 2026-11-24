<script setup lang="ts">
import { errorMessage } from '../format'
import UAlert from '@nuxt/ui/components/Alert.vue'
import UBadge from '@nuxt/ui/components/Badge.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { chatPostError } from '../api'
import { alertColor } from '../badgeTone'
import { getProblems, postTaskChat } from '../client'
import { live, useNow } from '../live'
import { localTimesText } from '../localTime'
import { needsOpen } from '../needsOpen'
import { pausesLine, pausesSummary } from '../pauses'
import { problemFromHash, problemParam, problemSubject, sameProblem, statusColors, statusLine } from '../problemThread'
import type { MilestoneView, ProblemView } from '../types'
import Markdown from './Markdown.vue'
import LocalText from './LocalText.vue'
import ProblemThread from './ProblemThread.vue'
import RelTime from './RelTime.vue'

// MilestoneNeeds is a milestone's Needs you tab (spec desk-problem-thread,
// Decision 11): the problems on the left, open first, and the chosen one's
// thread beside them from 1024 px (stacked below on smaller screens). The
// pilot's open proposals (P-n) with Accept and Reject and the pause totals
// stay under the list. Old #problem-N and #prompt-Qn links land on their
// problem's thread.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView; problem?: string }>()

const route = useRoute()
const router = useRouter()

const listed = ref<ProblemView[]>([])
const failed = ref('')
async function load() {
  try {
    listed.value = (await getProblems(props.namespace, props.name, props.milestone.iid)).problems
    failed.value = ''
  } catch (e) {
    failed.value = errorMessage(e)
  }
}
onMounted(load)
watch(
  () => live.version,
  () => void load(),
)

// problems: open ones first (the server's order within each).
const problems = computed(() => [...listed.value.filter((p) => p.state === 'open'), ...listed.value.filter((p) => p.state !== 'open')])
const open = computed(() => needsOpen(props.milestone))

const to = (p: ProblemView | string) => ({
  name: 'problem' as const,
  params: {
    namespace: props.namespace,
    name: props.name,
    iid: String(props.milestone.iid),
    problem: typeof p === 'string' ? p : problemParam(p),
  },
})

// selected: the route's problem, else the first open one.
const selected = computed(() => props.problem ?? (problems.value[0]?.state === 'open' ? problemParam(problems.value[0]) : undefined))

// An old anchor names a problem: open its thread instead.
watch(
  () => route.hash,
  (hash) => {
    const p = hash ? problemFromHash(hash, props.milestone.prompts) : undefined
    if (p) void router.replace(to(p))
  },
  { immediate: true },
)

const now = useNow()
const pauses = computed(() => pausesSummary(props.milestone.pauses, now.value))

// Accept and Reject send the pilot task the same chat command as the chat's
// buttons did; only owners with the chat grant get them.
const canAnswer = computed(() => !!props.milestone.permissions?.chat && !!props.milestone.pilot)
const sending = ref<number | null>(null)
const sendError = ref('')
async function decide(cmd: 'accept' | 'reject', proposal: number) {
  const task = props.milestone.pilot?.task
  if (!task || sending.value != null) return
  sending.value = proposal
  sendError.value = ''
  try {
    await postTaskChat(props.namespace, props.name, task, {
      body: JSON.stringify({ cmd, proposal }),
      clientKey: crypto.randomUUID(),
      kind: 'command',
    })
  } catch (e) {
    sendError.value = chatPostError(e)
  } finally {
    sending.value = null
  }
}

const sectionHead =
  'h3 m-0 mb-2 flex justify-between gap-2 text-[11.5px] font-semibold tracking-[0.06em] text-(--text-3) uppercase'
const quiet = 'text-[13px] text-(--text-3)'
const chip = { color: 'neutral', variant: 'outline', size: 'sm' } as const
</script>

<template>
  <div class="grid min-w-0 gap-[18px] min-[1024px]:grid-cols-[minmax(240px,300px)_minmax(0,1fr)]">
    <div class="grid min-w-0 content-start gap-[18px]">
      <section class="min-w-0" data-needs-problems>
        <h2 :class="sectionHead">
          <span>Problems</span>
          <span v-if="open.count">{{ open.count }}</span>
        </h2>
        <UAlert v-if="failed" :color="alertColor('danger')" variant="subtle" description="The problems could not be loaded." />
        <UEmpty v-else-if="!problems.length" icon="i-lucide-inbox" title="Nothing needs you." variant="naked" size="sm" />
        <nav v-else class="grid gap-1" aria-label="Problems">
          <RouterLink
            v-for="p in problems"
            :key="problemParam(p)"
            :to="to(p)"
            class="card grid min-w-0 gap-1 px-3 py-2 text-[13.5px]"
            :class="{ 'ring-2 ring-(--accent)': selected && sameProblem(selected, p), 'opacity-75': p.state !== 'open' }"
            :aria-current="selected && sameProblem(selected, p) ? 'page' : undefined"
            :data-problem="problemParam(p)"
          >
            <span class="flex min-w-0 items-baseline gap-2">
              <b class="shrink-0">{{ problemSubject(p) }}</b>
              <span class="min-w-0 truncate">{{ p.title }}</span>
            </span>
            <span class="flex min-w-0 flex-wrap items-center gap-2">
              <UBadge size="sm" variant="subtle" :color="statusColors[statusLine(p.status).tone]" :label="statusLine(p.status).text" />
              <RelTime class="faint text-[12px]" :time="p.resolvedAt ?? p.reopenedAt ?? p.openedAt" />
            </span>
            <span v-if="p.cause" class="min-w-0 truncate text-[12.5px] text-(--text-3)" :title="localTimesText(p.cause, now)"><LocalText :text="p.cause" /></span>
          </RouterLink>
        </nav>
      </section>

      <section v-if="open.proposals.length" class="min-w-0" data-needs-proposals>
        <h2 :class="sectionHead"><span>Proposals</span></h2>
        <div class="grid gap-2.5">
          <div v-for="p in open.proposals" :key="`P${p.id}`" class="card grid gap-2 px-3.5 py-3 text-[13.5px]" :data-proposal="p.id">
            <div class="flex flex-wrap items-baseline gap-x-2">
              <b>🧭 P{{ p.id }}</b>
              <RelTime v-if="p.postedAt" class="faint ml-auto text-[12px]" :time="p.postedAt" />
            </div>
            <!-- The summary is the pilot's untrusted text: markdown without HTML. -->
            <Markdown class="min-w-0 wrap-anywhere" :text="p.summary" />
            <div v-if="canAnswer" class="flex flex-wrap gap-1.5">
              <UButton v-bind="chip" label="Accept" :disabled="sending != null" @click="decide('accept', p.id)" />
              <UButton v-bind="chip" label="Reject" :disabled="sending != null" @click="decide('reject', p.id)" />
            </div>
          </div>
          <UAlert v-if="sendError" :color="alertColor('danger')" variant="subtle" :description="sendError" />
        </div>
      </section>

      <div v-if="pauses" :class="quiet" data-pauses>Pauses: {{ pausesLine(pauses) }}</div>
    </div>

    <ProblemThread
      v-if="selected"
      class="min-[1024px]:sticky min-[1024px]:top-2 min-[1024px]:h-[calc(100dvh-160px)]"
      :namespace="namespace"
      :name="name"
      :milestone="milestone"
      :problem="selected"
    />
    <UEmpty v-else class="self-start" icon="i-lucide-message-square" title="Choose a problem to see its thread." variant="naked" size="sm" />
  </div>
</template>
