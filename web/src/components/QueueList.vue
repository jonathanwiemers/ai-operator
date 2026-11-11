<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref, watch } from 'vue'
import { chatPostError } from '../api'
import { getTaskChat, postTaskChat } from '../client'
import { companionOf } from '../companion'
import { live } from '../live'
import { canRevisit, queueRows, queueTone, revisitCommand, revisitPromptID } from '../milestoneQueue'
import { commandAnswer, type CommandAnswer } from '../pauseCard'
import type { MilestoneView } from '../types'
import Truncated from '../ui/Truncated.vue'
import Badge from './Badge.vue'
import CompanionChip from './CompanionChip.vue'
import ExtLink from './ExtLink.vue'

// QueueList is a milestone's autopilot queue: per issue its state, merge
// request, implement task and the decisions recorded for it. Titles and
// decision titles are untrusted text: rendered as text only.
//
// Utilities only (docs/ui.md, "Milestone Pilot"): an item's position and
// number (never wrapped), its title (two lines, the rest on tap) and its
// state wrap onto lines of their own where the column is narrow. Links and
// Revisit are 44 px high on phones. The order is queueRows' order.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()

const rows = computed(() =>
  queueRows(props.milestone.autopilot?.items ?? [], props.milestone.spec.status.autopilot?.items),
)

// Revisit: owners send {cmd:'revisit', decision} to the pilot task (the
// target of milestone prompts); the operator's reply names the new prompt.
const pilotTask = computed(() => props.milestone.pilot?.task)
const sending = ref('')
const error = ref('')
const sent = ref<{ id: number; task: string; decision: string } | null>(null)
const answer = ref<CommandAnswer>({ state: 'pending' })
const promptID = computed(() => (answer.value.state === 'answered' ? revisitPromptID(answer.value.text) : ''))

async function revisit(decision: string) {
  const task = pilotTask.value
  if (!task || sending.value) return
  sending.value = decision
  error.value = ''
  try {
    const body = JSON.stringify(revisitCommand(decision))
    const r = await postTaskChat(props.namespace, props.name, task, { body, clientKey: crypto.randomUUID(), kind: 'command' })
    sent.value = { id: r.id, task, decision }
    answer.value = { state: 'pending' }
    void loadAnswer()
  } catch (e) {
    error.value = chatPostError(e)
  } finally {
    sending.value = ''
  }
}

async function loadAnswer() {
  const s = sent.value
  if (!s || answer.value.state !== 'pending') return
  try {
    const page = await getTaskChat(props.namespace, props.name, s.task, { limit: 50 })
    if (sent.value !== s) return
    answer.value = commandAnswer(page.messages, s.id)
  } catch {
    /* best-effort; the next live update tries again */
  }
}
watch(() => live.version, () => void loadAnswer())

// companion: the issue's companion merge request, if it has one.
const companion = (iid: number) => companionOf(props.milestone.companions, iid)

const issueURL = (iid: number) => props.milestone.webURL.replace(/\/-\/milestones\/\d+$/, `/-/issues/${iid}`)

// tap: a link in a row, at least 44 px high on phones.
const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
const note = 'text-[13px] text-(--text-3)'
</script>

<template>
  <ol class="m-0 grid list-none gap-0.5 p-0 text-[13px]" aria-label="Queue">
    <li v-for="(q, n) in rows" :key="q.iid" class="rounded-md px-1.5 py-1 hover:bg-(--surface-2)" data-queue-item>
      <div class="flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
        <span class="w-5 shrink-0 text-right tabular-nums text-(--text-3)" data-position>{{ n + 1 }}</span>
        <ExtLink :href="issueURL(q.iid)" class="font-mono text-xs whitespace-nowrap" :class="tap" data-iid>#{{ q.iid }}</ExtLink>
        <Truncated class="min-w-0 flex-[1_1_12rem] [overflow-wrap:anywhere]" :lines="2" :text="q.title" />
        <Badge class="ml-auto" :tone="queueTone(q.state)">{{ q.state.toLowerCase() }}</Badge>
      </div>
      <div
        v-if="q.mergeRequestIID || q.task || q.decisions.length || q.revisitOf || companion(q.iid)"
        class="ml-[26px] flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-(--text-3)"
      >
        <ExtLink v-if="q.mergeRequestURL" :href="q.mergeRequestURL" :class="tap">!{{ q.mergeRequestIID }}</ExtLink>
        <span v-else-if="q.mergeRequestIID">!{{ q.mergeRequestIID }}</span>
        <CompanionChip v-if="companion(q.iid)" :companion="companion(q.iid)!" :class="companion(q.iid)!.mrURL && tap" />
        <RouterLink v-if="q.task" :to="{ name: 'task', params: { namespace, name, task: q.task } }" :class="tap">
          task
        </RouterLink>
        <span v-if="q.revisitOf">revisits {{ q.revisitOf }}</span>
        <span
          v-for="d in q.decisions"
          :key="d.id"
          class="inline-flex items-center gap-1"
          :class="{ 'line-through opacity-55': d.superseded }"
          :title="d.title"
        >
          <ExtLink v-if="d.url" :href="d.url" :class="tap"><code>{{ d.id }}</code></ExtLink>
          <code v-else>{{ d.id }}</code>
          <UButton
            v-if="pilotTask && canRevisit(d, milestone.permissions)"
            color="neutral"
            variant="outline"
            size="xs"
            :disabled="!!sending"
            :label="sending === d.id ? '…' : 'Revisit'"
            @click="revisit(d.id)"
          />
        </span>
      </div>
    </li>
  </ol>
  <div v-if="error" :class="note" role="alert">{{ error }}</div>
  <div v-else-if="sent" :class="note" role="status">
    <template v-if="answer.state === 'pending'">Revisiting {{ sent.decision }}…</template>
    <template v-else-if="answer.state === 'failed'">{{ answer.reason }}</template>
    <template v-else>
      {{ answer.redacted ? `Revisit of ${sent.decision} opened.` : answer.text }}
      <a v-if="promptID" :href="`#prompt-${promptID}`" :class="tap">Go to {{ promptID }}</a>
    </template>
  </div>
  <div v-if="!rows.length" :class="note">The queue is empty.</div>
</template>
