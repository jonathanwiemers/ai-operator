<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { chatPostError, getTaskChat, postTaskChat } from '../api'
import type { ChatCommand } from '../chatState'
import { live } from '../live'
import {
  commandAnswer,
  pauseActions,
  pauseCommand,
  pauseEndsBy,
  pauseTitle,
  reviewLimitLines,
  type CommandAnswer,
  type PauseAction,
  type PauseActionID,
} from '../pauseCard'
import type { MilestoneView } from '../types'
import ExtLink from './ExtLink.vue'
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

// shown: the kind's actions this user can use now. Links need their target;
// commands the chat grant, and the implement task or the issue and head
// they name; Merge anyway the merge permission.
const all = computed(() => pauseActions(kind.value, issue.value || undefined))
const shown = computed<Shown[]>(() => {
  const out: Shown[] = []
  for (const a of all.value) {
    switch (a.id) {
      case 'openChat':
        if (implTaskName.value) out.push({ action: a, type: 'task', task: implTaskName.value })
        break
      case 'openPlan':
        out.push({ action: a, type: 'task', task: props.milestone.spec.name })
        break
      case 'openPipeline':
        if (pipelineURL.value) out.push({ action: a, type: 'ext', href: pipelineURL.value })
        break
      case 'openMR':
        if (item.value?.mergeRequestURL) out.push({ action: a, type: 'ext', href: item.value.mergeRequestURL })
        break
      default:
        if (!canChat.value) break
        if (a.target === 'implement' && !implTaskName.value) break
        if (a.ownerOnly && !canMerge.value) break
        if (a.id === 'skip' && !issue.value) {
          if (skipOptions.value.length) out.push({ action: a, type: 'skipSelect' })
          break
        }
        if (pauseCommand(a, { issue: issue.value, head: head.value })) out.push({ action: a, type: 'command' })
    }
  }
  return out
})
const mergeAction = computed(() => shown.value.find((s) => s.action.confirm)?.action)
// The kind has commands this user cannot send without the chat grant.
const commandsHidden = computed(() => !canChat.value && all.value.some((a) => a.target !== 'link'))

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
    </div>

    <ul v-if="limitLines.length" class="limits">
      <li v-for="l in limitLines" :key="l">{{ l }}</li>
    </ul>
    <p v-if="endsBy" class="faint">{{ endsBy }}</p>
    <div v-if="ap.waitingFor" class="banner info">A run is working on #{{ ap.waitingFor }} now.</div>
    <p v-if="override" class="muted">
      Merge anyway was given by {{ override.by }} <RelTime :time="override.at" />.
    </p>

    <div v-if="shown.length" class="row actions">
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
    <p v-if="commandsHidden" class="faint">
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
