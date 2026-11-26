<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { chatPostError } from '../api'
import { alertColor } from '../badgeTone'
import { useChatSend } from '../chatSend'
import { postTaskChat } from '../client'
import type { LooseIssues } from '../generated/views'
import { useNow } from '../live'
import { looseAnchor, queueNeeds, roundLine } from '../looseIssues'
import Composer, { type ComposerSend } from './Composer.vue'
import ExtLink from './ExtLink.vue'
import Markdown from './Markdown.vue'
import ProblemFacts from './ProblemFacts.vue'
import PromptCard from './PromptCard.vue'
import RelTime from './RelTime.vue'

// LooseNeeds is the top of the Loose issues tab (R6 of "a pilot for the
// queue"): the pilot's round line, the queue's problem cards (the
// Cockpit's card pieces: facts and prompts answered in place), the pilot's
// open proposals with Accept and Reject, and the composer to the queue's
// pilot for its owners (take). Each card has an id (looseAnchor) the
// Inbox links to; the route's hash scrolls it into view once it is shown.
// The problem thread history of a milestone's cards is not here: the
// pilot's answers are on its task page.
const props = defineProps<{ namespace: string; name: string; view: LooseIssues; take: boolean }>()
const emit = defineEmits<{ sent: [] }>()

const now = useNow()
const round = computed(() => roundLine(props.view.round, now.value))
const needs = computed(() => queueNeeds(props.view))
const pilot = computed(() => props.view.pilotTask ?? '')
const canWrite = computed(() => props.take && !!pilot.value)

// Each navigation scrolls to the hash's card once, a repeated link to the
// same card too: vue-router does not change the route then, but runs the
// afterEach hooks (navs) and the router's scroll, which this one follows
// a frame later.
const route = useRoute()
const navs = ref(0)
onBeforeUnmount(useRouter().afterEach(() => void navs.value++))
const root = ref<HTMLElement | null>(null)
let scrolled = ''
watch(
  () => [route.hash, navs.value, needs.value, root.value] as const,
  async ([hash, nav]) => {
    const key = `${nav}${hash}`
    if (!hash || key === scrolled) return
    await nextTick()
    await new Promise(requestAnimationFrame)
    const id = hash.slice(1)
    const el = root.value?.id === id ? root.value : root.value?.querySelector(`[id="${CSS.escape(id)}"]`)
    if (!el || key === scrolled) return
    scrolled = key
    el.scrollIntoView({ block: 'nearest' })
  },
  { immediate: true },
)

// Accept and Reject send the pilot the chat command the Cockpit's do.
const deciding = ref<number | null>(null)
const decideError = ref('')
async function decide(cmd: 'accept' | 'reject', proposal: number) {
  if (!pilot.value || deciding.value != null) return
  deciding.value = proposal
  decideError.value = ''
  try {
    await postTaskChat(props.namespace, props.name, pilot.value, { body: JSON.stringify({ cmd, proposal }), clientKey: crypto.randomUUID(), kind: 'command' })
    emit('sent')
  } catch (e) {
    decideError.value = chatPostError(e)
  } finally {
    deciding.value = null
  }
}

const sentOnce = ref(false)
const { body, sending, sendError, post } = useChatSend(
  () => ({ namespace: props.namespace, name: props.name, task: pilot.value }),
  () => {
    sentOnce.value = true
    emit('sent')
  },
)
function onSend(s: ComposerSend) {
  if (canWrite.value) void post(s.kind, s.body)
}

const chip = { color: 'neutral', variant: 'outline', size: 'sm' } as const
</script>

<template>
  <!-- The queue's own card is loose-0; without one the top of the tab is. -->
  <div ref="root" :id="needs.problems.some((p) => !p.iid) ? undefined : looseAnchor({ issue: 0 })" class="grid min-w-0 gap-4" data-loose-needs>
    <p v-if="round" class="m-0 text-(--text-2) tabular-nums" data-loose-round>{{ round }}</p>

    <section v-if="needs.count" class="grid min-w-0 gap-2" aria-labelledby="loose-problems">
      <h2 id="loose-problems" class="m-0 text-sm font-semibold">Needs you ({{ needs.count }})</h2>
      <article
        v-for="p in needs.problems"
        :id="looseAnchor({ issue: p.iid })"
        :key="p.iid"
        class="card grid min-w-0 gap-2 px-3.5 py-3 text-[13.5px]"
        :aria-label="p.iid ? `#${p.iid} ${p.title}` : p.title"
        :data-loose-problem="p.iid"
      >
        <div class="flex min-w-0 flex-wrap items-baseline gap-x-2">
          <ExtLink v-if="p.iid && p.url" :href="p.url" class="font-mono text-xs max-tablet:min-h-6 max-tablet:min-w-6">#{{ p.iid }}</ExtLink>
          <b class="min-w-0 wrap-anywhere">{{ p.title }}</b>
        </div>
        <p v-if="p.cause" class="m-0 min-w-0 font-medium wrap-anywhere" data-loose-cause>{{ p.cause }}</p>
        <ProblemFacts v-if="p.facts" :namespace="namespace" :name="name" :facts="p.facts" />
        <PromptCard v-for="q in p.prompts" :key="q.id" :namespace="namespace" :name="name" :prompt="q" :facts="p.facts" />
      </article>
      <article
        v-for="p in needs.proposals"
        :id="looseAnchor({ proposal: p.id })"
        :key="`P${p.id}`"
        class="card grid min-w-0 gap-2 px-3.5 py-3 text-[13.5px]"
        :aria-label="`Proposal P${p.id}`"
        :data-proposal="p.id"
      >
        <div class="flex flex-wrap items-baseline gap-x-2">
          <b>🧭 P{{ p.id }}</b>
          <RelTime v-if="p.postedAt" class="faint ml-auto text-[12px]" :time="p.postedAt" />
        </div>
        <!-- The summary is the pilot's untrusted text: markdown without HTML. -->
        <Markdown class="min-w-0 wrap-anywhere" :text="p.summary" />
        <div v-if="canWrite" class="flex flex-wrap gap-1.5">
          <UButton v-bind="chip" label="Accept" :disabled="deciding != null" @click="decide('accept', p.id)" />
          <UButton v-bind="chip" label="Reject" :disabled="deciding != null" @click="decide('reject', p.id)" />
        </div>
      </article>
      <UAlert v-if="decideError" :color="alertColor('danger')" variant="subtle" :description="decideError" />
    </section>

    <section v-if="pilot" class="grid min-w-0 gap-1.5" aria-label="Message the pilot" data-loose-composer>
      <div class="flex flex-wrap items-center gap-x-2 text-[13px]">
        <span class="faint">Pilot</span>
        <RouterLink class="ml-auto inline-flex min-h-11 items-center tablet:min-h-0" :to="{ name: 'task', params: { namespace, name, task: pilot } }">Pilot task</RouterLink>
      </div>
      <Composer
        v-if="canWrite"
        v-model="body"
        mode="chat"
        placeholder="Message the queue's pilot…"
        hint="Enter+Ctrl/Cmd to send"
        can-interrupt
        :busy="sending"
        :disabled="sending"
        :error="sendError"
        @send="onSend"
      />
      <p v-else class="m-0 text-[13px] text-(--text-3)" data-loose-pilot-hint>
        Read-only: only the queue's owners can message the pilot.
      </p>
      <p v-if="sentOnce" class="m-0 text-[12.5px] text-(--text-3)" role="status" data-loose-sent>
        Sent. The pilot answers on its task page.
      </p>
    </section>
  </div>
</template>
