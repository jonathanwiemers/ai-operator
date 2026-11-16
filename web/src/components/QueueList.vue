<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref, watch } from 'vue'
import { chatPostError } from '../api'
import type { ChatCommand } from '../chatState'
import { getTaskChat, postTaskChat } from '../client'
import { companionOf } from '../companion'
import { live } from '../live'
import {
  canRevisit,
  laneGroups,
  parallelCommand,
  parallelMax,
  parallelMin,
  queueRows,
  queueTone,
  resumeCommand,
  revisitCommand,
  revisitPromptID,
  waitingLabel,
  waveChip,
  wavesLabel,
} from '../milestoneQueue'
import { commandAnswer, type CommandAnswer } from '../pauseCard'
import type { MilestoneView } from '../types'
import Truncated from '../ui/Truncated.vue'
import Badge from './Badge.vue'
import CompanionChip from './CompanionChip.vue'
import ExtLink from './ExtLink.vue'

// QueueList is a milestone's autopilot queue: per issue its state, merge
// request, implement task and the decisions recorded for it, what a waiting
// issue waits for and why a held one is held. Titles, decision titles and
// hold reasons are untrusted text: rendered as text only.
//
// Utilities only (docs/contributing/web-ui.md, "Milestone Pilot"): an item's position and
// number (never wrapped), its title (two lines, the rest on tap) and its
// state wrap onto lines of their own where the column is narrow; a planned
// item shows its wave chip (W1, W2, …) after its number. Links, Revisit,
// Resume and the stepper's buttons are 44 px high on phones. The order is queueRows'
// order; grouped (the Issues tab), the items are in lanes (laneGroups) and
// keep their queue position.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView; grouped?: boolean }>()

const rows = computed(() =>
  queueRows(props.milestone.autopilot?.items ?? [], props.milestone.spec.status.autopilot?.items).map((q, n) => ({
    ...q,
    position: n + 1,
  })),
)
const groups = computed(() => {
  const a = props.milestone.autopilot
  return props.grouped && a ? laneGroups(rows.value, a.running, a.maxParallel) : [{ lane: '', label: '', items: rows.value }]
})

// Revisit: owners send {cmd:'revisit', decision} to the pilot task (the
// target of milestone prompts); the operator's reply names the new prompt.
// Resume: viewers who may chat send {cmd:'resume', issue} to the spec task,
// as the pause card's Resume does. One command at a time; the status line
// follows the last one sent.
const pilotTask = computed(() => props.milestone.pilot?.task)
const canResume = computed(() => !!props.milestone.permissions?.chat)
const sending = ref('')
const error = ref('')
type Sent = { id: number; task: string; key: string; kind: ChatCommand['cmd'] }
const sent = ref<Sent | null>(null)
const answer = ref<CommandAnswer>({ state: 'pending' })
const promptID = computed(() =>
  sent.value?.kind === 'revisit' && answer.value.state === 'answered' ? revisitPromptID(answer.value.text) : '',
)

async function send(task: string | undefined, cmd: ChatCommand, key: string): Promise<boolean> {
  if (!task || sending.value) return false
  sending.value = key
  error.value = ''
  try {
    const body = JSON.stringify(cmd)
    const r = await postTaskChat(props.namespace, props.name, task, { body, clientKey: crypto.randomUUID(), kind: 'command' })
    sent.value = { id: r.id, task, key, kind: cmd.cmd }
    answer.value = { state: 'pending' }
    void loadAnswer()
    return true
  } catch (e) {
    error.value = chatPostError(e)
    return false
  } finally {
    sending.value = ''
  }
}

const revisit = (decision: string) => send(pilotTask.value, revisitCommand(decision), decision)
const resume = (iid: number) => send(props.milestone.spec.name, resumeCommand(iid), `#${iid}`)

// The limit stepper (grouped, owners: the parallel grant): − and + send
// {cmd:'parallel', parallel} to the spec task, as `/agent parallel N`.
// The new limit shows as pending until the view's maxParallel reports it,
// or until the post or the operator's answer fails.
const maxParallel = computed(() => props.milestone.autopilot?.maxParallel ?? 0)
const canParallel = computed(() => !!props.grouped && !!props.milestone.permissions?.parallel && maxParallel.value > 0)
const pendingLimit = ref<number | null>(null)
const limit = computed(() => pendingLimit.value ?? maxParallel.value)
// Also when a step goes back to the limit the view has (+ then −).
watch([maxParallel, pendingLimit], ([n, p]) => {
  if (n === p) pendingLimit.value = null
})
watch(answer, (a) => {
  if (a.state === 'failed' && sent.value?.kind === 'parallel') pendingLimit.value = null
})
async function step(by: 1 | -1) {
  const cmd = parallelCommand(limit.value, by)
  if (!cmd || cmd.cmd !== 'parallel') return
  const before = pendingLimit.value
  pendingLimit.value = cmd.parallel
  if (!(await send(props.milestone.spec.name, cmd, String(cmd.parallel)))) pendingLimit.value = before
}
// sentLine is the status line of the last command, pending or sent (an
// answer the viewer may not read).
function sentLine(s: Sent, a: CommandAnswer): string {
  const pending = a.state === 'pending'
  if (s.kind === 'revisit') return pending ? `Revisiting ${s.key}…` : `Revisit of ${s.key} opened.`
  if (s.kind === 'parallel') return pending ? `Setting the limit to ${s.key}…` : `Limit ${s.key} sent.`
  return pending ? `Resuming ${s.key}…` : `Resume ${s.key} sent.`
}

// waves: "N waves" for a plan of several layers (display only).
const waves = computed(() => wavesLabel(props.milestone.autopilot?.items ?? []))

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
// laneHead: a lane's heading, spaced from the lane above it.
const laneHead = 'm-0 mb-1 px-1.5 text-xs font-semibold text-(--text-2)'
</script>

<template>
  <p v-if="grouped && waves" :class="[laneHead, 'mt-0 mb-2 font-normal text-(--text-3)']" data-waves>{{ waves }}</p>
  <template v-for="(g, n) in groups" :key="g.lane">
    <h4
      v-if="g.lane === 'running' && canParallel"
      :class="[laneHead, n ? 'mt-3' : 'mt-0', 'flex flex-wrap items-center gap-x-1']"
      data-lane-head
    >
      <span>Running {{ milestone.autopilot?.running ?? 0 }} of</span>
      <span class="inline-flex items-center gap-0.5" role="group" aria-label="Parallel limit" data-stepper>
        <UButton
          icon="i-lucide-minus"
          color="neutral"
          variant="outline"
          size="xs"
          class="min-h-11 min-w-11 justify-center tablet:min-h-0 tablet:min-w-0"
          :disabled="!!sending || limit <= parallelMin"
          aria-label="Fewer at a time"
          @click="step(-1)"
        />
        <span
          class="min-w-6 text-center tabular-nums"
          :class="pendingLimit !== null && 'text-(--text-3) italic'"
          :title="pendingLimit !== null ? `Pending: ${pendingLimit} at a time` : `${limit} at a time`"
          data-limit
        >{{ limit }}</span>
        <UButton
          icon="i-lucide-plus"
          color="neutral"
          variant="outline"
          size="xs"
          class="min-h-11 min-w-11 justify-center tablet:min-h-0 tablet:min-w-0"
          :disabled="!!sending || limit >= parallelMax"
          aria-label="More at a time"
          @click="step(1)"
        />
      </span>
    </h4>
    <h4 v-else-if="g.label" :class="[laneHead, n ? 'mt-3' : 'mt-0']" data-lane-head>{{ g.label }}</h4>
    <ol
      v-if="g.items.length || !g.label"
      class="m-0 grid list-none gap-0.5 p-0 text-[13px]"
      :aria-label="g.label || 'Queue'"
      :data-lane="g.lane || undefined"
    >
      <li v-for="q in g.items" :key="q.iid" class="rounded-md px-1.5 py-1 hover:bg-(--surface-2)" data-queue-item>
        <div class="flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
          <span class="w-5 shrink-0 text-right tabular-nums text-(--text-3)" data-position>{{ q.position }}</span>
          <ExtLink :href="issueURL(q.iid)" class="font-mono text-xs whitespace-nowrap" :class="tap" data-iid>#{{ q.iid }}</ExtLink>
          <span
            v-if="waveChip(q.wave)"
            class="rounded-full bg-(--surface-3) px-1.5 text-[11px] leading-[18px] font-semibold whitespace-nowrap tabular-nums text-(--text-2)"
            :title="`Wave ${q.wave} of the plan`"
            data-wave
          >{{ waveChip(q.wave) }}</span>
          <Truncated class="min-w-0 flex-[1_1_12rem] [overflow-wrap:anywhere]" :lines="2" :text="q.title" />
          <Badge class="ml-auto" :tone="queueTone(q.state)">{{ q.state.toLowerCase() }}</Badge>
        </div>
        <div
          v-if="q.mergeRequestIID || q.task || q.decisions.length || q.revisitOf || companion(q.iid) || q.lane === 'waiting'"
          class="ml-[26px] flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-(--text-3)"
        >
          <span v-if="q.lane === 'waiting'" data-waiting>{{ waitingLabel(q.waitingFor) }}</span>
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
        <div v-if="q.lane === 'held' && q.hold" class="ml-[26px] flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs" data-hold>
          <span class="min-w-0 flex-[1_1_12rem] [overflow-wrap:anywhere] text-(--tone-warning-fg)">{{ q.hold.reason || 'Held.' }}</span>
          <UButton
            v-if="canResume"
            color="neutral"
            variant="outline"
            size="xs"
            class="min-h-11 justify-center tablet:min-h-0"
            :disabled="!!sending"
            :label="sending === `#${q.iid}` ? '…' : 'Resume'"
            :aria-label="`Resume #${q.iid}`"
            @click="resume(q.iid)"
          />
        </div>
      </li>
    </ol>
  </template>
  <div v-if="error" :class="note" role="alert">{{ error }}</div>
  <div v-else-if="sent" :class="note" role="status">
    <template v-if="answer.state === 'pending'">{{ sentLine(sent, answer) }}</template>
    <template v-else-if="answer.state === 'failed'">{{ answer.reason }}</template>
    <template v-else-if="answer.redacted">{{ sentLine(sent, answer) }}</template>
    <template v-else>
      {{ answer.text }}
      <a v-if="promptID" :href="`#prompt-${promptID}`" :class="tap">Go to {{ promptID }}</a>
    </template>
  </div>
  <div v-if="!rows.length" :class="note">The queue is empty.</div>
</template>
