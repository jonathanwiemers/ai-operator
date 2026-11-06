<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { chatPostError, getTaskChat, postTaskChat } from '../api'
import type { ChatCommand } from '../chatState'
import { companionOf } from '../companion'
import { live } from '../live'
import {
  commandAnswer,
  deskCommand,
  pauseActions,
  pauseCommand,
  pauseEndsBy,
  pausePrompt,
  pauseTitle,
  reviewLimitLines,
  wayOut,
  type CommandAnswer,
  type PauseAction,
  type PauseActionID,
} from '../pauseCard'
import type { MilestoneView } from '../types'
import CompanionChip from './CompanionChip.vue'
import ExtLink from './ExtLink.vue'
import PromptCard from './PromptCard.vue'
import RelTime from './RelTime.vue'

// PauseCard shows why a milestone's autopilot paused and the actions that
// fix it (spec desk-pause-card, section 3). Commands go through the Desk
// chat of the task that owns them; the operator's answer shows in the card.
// The pause reason and issue titles are untrusted text: rendered as text.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()

const ap = computed(() => props.milestone.autopilot)
const kind = computed(() => ap.value?.pauseKind)
const issue = computed(() => ap.value?.pausedFor || 0)
const head = computed(() => (ap.value?.head || '').trim())
const item = computed(() => (issue.value ? ap.value?.items.find((i) => i.iid === issue.value) : undefined))
// implTask: the paused issue's implement task (the queue knows it; the
// milestone's tasks are the fallback).
const implTask = computed(() => {
  if (!issue.value) return undefined
  return props.milestone.tasks.find((t) =>
    item.value?.task ? t.name === item.value.task : t.type === 'Implement' && t.issue?.iid === issue.value,
  )
})
const implTaskName = computed(() => item.value?.task || implTask.value?.name)
const issueTitle = computed(() => item.value?.title || implTask.value?.issue?.title)
const issueURL = computed(() =>
  props.milestone.webURL.replace(/\/-\/milestones\/\d+$/, `/-/issues/${issue.value}`),
)
const pipelineURL = computed(() => implTask.value?.status.ci?.pipelineURL)
// companion: the paused issue's companion merge request (kind companion).
const companion = computed(() => companionOf(props.milestone.companions, issue.value))

// prompt: the open question this pause asks (mainRed, budget); it replaces
// the bare actions.
const prompt = computed(() => pausePrompt(kind.value, props.milestone.prompts))

const canChat = computed(() => !!props.milestone.permissions?.chat)
const canMerge = computed(() => !!props.milestone.permissions?.merge)
const endsBy = computed(() => pauseEndsBy(kind.value, issue.value || undefined))
const limitLines = computed(() => (kind.value === 'reviewErrors' ? reviewLimitLines(ap.value?.reviewErrors) : []))
const override = computed(() => {
  const o = ap.value?.mergeOverride
  return o && o.issueIID === issue.value && head.value && o.sha === head.value ? o : undefined
})

// Skip without a paused issue (cycle, stuck) names one from the queue.
const skipIssue = ref<number | ''>('')
const skipOptions = computed(() =>
  (ap.value?.items ?? []).filter((i) => i.state === 'Pending' || i.state === 'Active'),
)

type Shown =
  | { action: PauseAction; type: 'task'; task: string }
  | { action: PauseAction; type: 'ext'; href: string }
  | { action: PauseAction; type: 'command' }
  | { action: PauseAction; type: 'skipSelect' }

// usable is an action as this user can use it now, undefined when not.
// Links need their target; commands the chat grant, and the implement task
// or the issue and head they name; Merge anyway the merge permission.
function usable(a: PauseAction): Shown | undefined {
  switch (a.id) {
    case 'openChat':
      return implTaskName.value ? { action: a, type: 'task', task: implTaskName.value } : undefined
    case 'openPlan':
      return { action: a, type: 'task', task: props.milestone.spec.name }
    case 'openPipeline':
      return pipelineURL.value ? { action: a, type: 'ext', href: pipelineURL.value } : undefined
    case 'openMR':
      return item.value?.mergeRequestURL ? { action: a, type: 'ext', href: item.value.mergeRequestURL } : undefined
    case 'openCompanion':
      return companion.value?.mrURL ? { action: a, type: 'ext', href: companion.value.mrURL } : undefined
  }
  if (!canChat.value) return undefined
  if (a.target === 'implement' && !implTaskName.value) return undefined
  if (a.ownerOnly && !canMerge.value) return undefined
  if (a.id === 'skip' && !issue.value) return skipOptions.value.length ? { action: a, type: 'skipSelect' } : undefined
  return pauseCommand(a, { issue: issue.value, head: head.value }) ? { action: a, type: 'command' } : undefined
}

// exits: the ways out (an operator without them gets the kind's buttons).
// The action row then keeps the links; the remedies bring the commands.
const exits = computed(() => ap.value?.exits || [])
const way = computed(() => wayOut(exits.value))
const remedyLinks = computed(() => ({
  milestone: props.milestone.webURL,
  mergeRequest: item.value?.mergeRequestURL,
  issue: issue.value ? issueURL.value : undefined,
  companion: companion.value?.mrURL,
}))
const remedies = computed(() =>
  way.value.remedies.map((r) => ({
    ...r,
    shown: r.action ? usable(r.action) : undefined,
    href: r.link ? remedyLinks.value[r.link] : undefined,
  })),
)
const all = computed(() => pauseActions(kind.value, issue.value || undefined, exits.value))
const shown = computed(() => all.value.map(usable).filter((s): s is Shown => !!s))
const mergeAction = computed(
  () => [...shown.value, ...remedies.value.map((r) => r.shown)].find((s) => s?.action.confirm)?.action,
)
// The pause has commands this user cannot send without the chat grant.
const commandsHidden = computed(
  () =>
    !canChat.value &&
    (exits.value.length
      ? way.value.remedies.some((r) => r.kind === 'command' && deskCommand(r.command))
      : all.value.some((a) => a.target !== 'link')),
)

// Sending: one command at a time; the card follows the last one sent.
const sending = ref<PauseActionID | null>(null)
const postError = ref('')
const sent = ref<{ id: number; task: string; label: string } | null>(null)
const answer = ref<CommandAnswer>({ state: 'pending' })
// A failed post keeps its key: retrying the same body reuses it.
let unsent: { task: string; body: string; clientKey: string } | null = null
let seq = 0

// Merge anyway confirms first, naming the head it will send.
const confirmHead = ref('')

function label(a: PauseAction, cmd: ChatCommand): string {
  return cmd.cmd === 'skip' ? `Skip #${cmd.issue}` : a.label
}

async function send(a: PauseAction, cmd: ChatCommand) {
  if (sending.value) return
  const task = a.target === 'implement' ? implTaskName.value : props.milestone.spec.name
  if (!task) return
  const body = JSON.stringify(cmd)
  const clientKey =
    unsent && unsent.task === task && unsent.body === body ? unsent.clientKey : crypto.randomUUID()
  unsent = { task, body, clientKey }
  sending.value = a.id
  postError.value = ''
  try {
    const r = await postTaskChat(props.namespace, props.name, task, { body, clientKey, kind: 'command' })
    unsent = null
    confirmHead.value = ''
    sent.value = { id: r.id, task, label: label(a, cmd) }
    answer.value = { state: 'pending' }
    void loadAnswer()
  } catch (e) {
    postError.value = chatPostError(e)
  } finally {
    sending.value = null
  }
}

function run(a: PauseAction) {
  if (a.confirm) {
    confirmHead.value = head.value
    return
  }
  const cmd = pauseCommand(a, { issue: issue.value, head: head.value })
  if (cmd) void send(a, cmd)
}

function confirmMerge(a: PauseAction) {
  const cmd = pauseCommand(a, { issue: issue.value, head: confirmHead.value })
  if (cmd) void send(a, cmd)
}

function skipPicked(a: PauseAction) {
  const n = Number(skipIssue.value)
  const cmd = pauseCommand(a, { issue: n })
  if (cmd) void send(a, cmd)
}

// loadAnswer looks for the operator's answer in the command's task chat;
// live updates bring it (the reply lands before the task changes).
async function loadAnswer() {
  const s = sent.value
  if (!s || answer.value.state !== 'pending') return
  const mine = ++seq
  try {
    const page = await getTaskChat(props.namespace, props.name, s.task, { limit: 50 })
    if (mine !== seq || sent.value !== s) return
    answer.value = commandAnswer(page.messages, s.id)
  } catch {
    /* best-effort; the next live update tries again */
  }
}

watch(
  () => live.version,
  () => void loadAnswer(),
)
</script>

<template>
  <section v-if="ap" class="card pause">
    <h2>{{ pauseTitle(kind) }}</h2>
    <p v-if="ap.pausedReason" class="text">{{ ap.pausedReason }}</p>
    <p v-else-if="!kind" class="muted">Waiting for a human.</p>

    <div v-if="issue" class="row issue">
      <ExtLink :href="issueURL">#{{ issue }}</ExtLink>
      <span v-if="issueTitle" class="title">{{ issueTitle }}</span>
      <ExtLink v-if="item?.mergeRequestURL" :href="item.mergeRequestURL">
        {{ item.mergeRequestIID ? `!${item.mergeRequestIID}` : 'MR' }}
      </ExtLink>
      <code v-if="head" class="faint" :title="head">{{ head.slice(0, 7) }}</code>
      <CompanionChip v-if="companion" :companion="companion" />
    </div>

    <ul v-if="limitLines.length" class="limits">
      <li v-for="l in limitLines" :key="l">{{ l }}</li>
    </ul>
    <p v-if="endsBy && !exits.length" class="faint">{{ endsBy }}</p>
    <div v-if="ap.waitingFor" class="banner info">A run is working on #{{ ap.waitingFor }} now.</div>
    <p v-if="override" class="muted">
      Merge anyway was given by {{ override.by }} <RelTime :time="override.at" />.
    </p>

    <div v-if="exits.length" class="way">
      <h3>Way out</h3>
      <ul v-if="way.auto.length" class="exits">
        <li v-for="e in way.auto" :key="e.id">
          <span class="row">
            <span class="when">{{ e.when }}</span>
            <span class="chip" :class="e.tone">{{ e.chip }}</span>
          </span>
          <span v-if="e.detail" class="muted detail">{{ e.detail }}</span>
          <span class="faint">→ {{ e.does }}</span>
        </li>
      </ul>
      <ul v-if="way.remedies.length" class="exits">
        <li v-for="r in remedies" :key="r.id">
          <span class="row">
            <span v-if="r.shown?.type === 'skipSelect'" class="row skip">
              <select v-model="skipIssue" aria-label="Issue to skip" :disabled="!!sending">
                <option value="">issue…</option>
                <option v-for="it in skipOptions" :key="it.iid" :value="it.iid">#{{ it.iid }} {{ it.title }}</option>
              </select>
              <button type="button" class="btn" :disabled="!!sending || !skipIssue" @click="skipPicked(r.shown.action)">
                {{ sending === r.shown.action.id ? '…' : r.shown.action.label }}
              </button>
            </span>
            <button
              v-else-if="r.shown"
              type="button"
              class="btn"
              :disabled="!!sending || (r.shown.action.confirm && !!confirmHead)"
              @click="run(r.shown.action)"
            >
              {{ sending === r.shown.action.id ? '…' : r.shown.action.label }}
            </button>
            <code v-else-if="r.elsewhere">{{ r.command }}</code>
            <span v-if="r.shown || !r.elsewhere" class="when">{{ r.text }}</span>
            <span v-else class="when">{{ r.elsewhere }}</span>
            <ExtLink v-if="r.href" :href="r.href">open</ExtLink>
          </span>
          <span class="faint">→ {{ r.does }}</span>
        </li>
      </ul>
    </div>

    <PromptCard v-if="prompt" :namespace="namespace" :name="name" :prompt="prompt" />

    <div v-else-if="shown.length" class="row actions">
      <template v-for="s in shown" :key="s.action.id">
        <RouterLink v-if="s.type === 'task'" class="btn" :to="{ name: 'task', params: { namespace, name, task: s.task } }">
          {{ s.action.label }}
        </RouterLink>
        <ExtLink v-else-if="s.type === 'ext'" class="btn" :href="s.href">{{ s.action.label }}</ExtLink>
        <span v-else-if="s.type === 'skipSelect'" class="row skip">
          <select v-model="skipIssue" aria-label="Issue to skip" :disabled="!!sending">
            <option value="">issue…</option>
            <option v-for="it in skipOptions" :key="it.iid" :value="it.iid">#{{ it.iid }} {{ it.title }}</option>
          </select>
          <button type="button" class="btn" :disabled="!!sending || !skipIssue" @click="skipPicked(s.action)">
            {{ sending === s.action.id ? '…' : s.action.label }}
          </button>
        </span>
        <button
          v-else
          type="button"
          class="btn"
          :disabled="!!sending || (s.action.confirm && !!confirmHead)"
          @click="run(s.action)"
        >
          {{ sending === s.action.id ? '…' : s.action.label }}
        </button>
      </template>
    </div>

    <div v-if="mergeAction && confirmHead" class="banner warning confirm">
      <span>
        Merge <code>{{ confirmHead.slice(0, 7) }}</code> without a full review? CI, MR state and approvals still apply.
      </span>
      <span class="row">
        <button type="button" class="btn primary" :disabled="!!sending" @click="confirmMerge(mergeAction)">
          {{ sending === mergeAction.id ? '…' : 'Confirm' }}
        </button>
        <button type="button" class="btn" :disabled="!!sending" @click="confirmHead = ''">Cancel</button>
      </span>
    </div>

    <p v-if="sent" class="answer">
      <strong>{{ sent.label }}:</strong>
      <span v-if="answer.state === 'pending'" class="faint"> sent…</span>
      <span v-else-if="answer.state === 'failed'" class="err"> {{ answer.reason }}</span>
      <span v-else-if="answer.redacted" class="muted"> answered</span>
      <span v-else class="text"> {{ answer.text }}</span>
    </p>
    <p v-if="postError" class="err">{{ postError }}</p>
    <p v-if="commandsHidden && !prompt" class="faint">
      Commands need Desk chat, or <code>/agent …</code> on the
      <ExtLink v-if="milestone.tracking" :href="milestone.tracking.webURL">tracking issue</ExtLink>
      <template v-else>tracking issue</template>.
    </p>
  </section>
</template>

<style scoped>
.pause {
  margin-top: 12px;
  padding: 14px 16px;
  border-left: 3px solid var(--tone-warning-fg);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.pause p,
.pause ul {
  margin: 0;
}

.limits {
  padding-left: 18px;
}

.title {
  overflow-wrap: anywhere;
}

.banner {
  margin-top: 0;
}

.actions {
  margin-top: 4px;
}

.way h3 {
  margin: 4px 0;
  font-size: 14px;
}

.exits {
  list-style: none;
  padding: 0;
  margin: 0 0 8px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.exits li {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.exits .row {
  gap: 8px;
  flex-wrap: wrap;
}

.when,
.detail {
  overflow-wrap: anywhere;
}

.chip {
  font-size: 12px;
  padding: 1px 8px;
  border-radius: 999px;
  white-space: nowrap;
}

.chip.success {
  background: var(--tone-success-bg);
  color: var(--tone-success-fg);
}

.chip.progress {
  background: var(--tone-progress-bg);
  color: var(--tone-progress-fg);
}

.chip.muted {
  background: var(--tone-muted-bg);
  color: var(--tone-muted-fg);
}

.skip {
  gap: 6px;
}

select {
  font: inherit;
  height: 34px;
  padding: 0 8px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  max-width: 28ch;
}

.btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.confirm {
  align-items: center;
}

.text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.err {
  color: var(--tone-danger-fg);
}
</style>
