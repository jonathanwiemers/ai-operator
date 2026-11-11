<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref, watch } from 'vue'
import { chatPostError, getTaskChat, postTaskChat } from '../api'
import type { ChatCommand } from '../chatState'
import { companionOf } from '../companion'
import { safeURL } from '../format'
import { live } from '../live'
import {
  commandAnswer,
  deskCommand,
  pauseActions,
  pauseCommand,
  agentCommand,
  pauseEndsBy,
  pauseExplain,
  pausePrompt,
  pauseRelease,
  pauseTitle,
  reviewLimitLines,
  wayOut,
  type CommandAnswer,
  type PauseAction,
  type PauseActionID,
} from '../pauseCard'
import type { MilestoneView } from '../types'
import Badge from './Badge.vue'
import CompanionChip from './CompanionChip.vue'
import ExtLink from './ExtLink.vue'
import Markdown from './Markdown.vue'
import PromptCard from './PromptCard.vue'
import RelTime from './RelTime.vue'

// PauseCard shows why a milestone's autopilot paused and the actions that
// fix it (spec desk-pause-card, section 3). Commands go through the Desk
// chat of the task that owns them; the operator's answer shows in the card.
// The pause reason is untrusted text: block markdown (markdown.ts, no HTML);
// issue titles are plain text.
//
// Utilities only (docs/ui.md, "Pause cards"): the actions are UButtons, at
// least 44 px high on phones, where the action row and the Way out's rows
// stack into columns and the skip select is 16 px.
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
// release: the final release record of a release pause.
const release = computed(() => pauseRelease(kind.value, props.milestone.releases))
// releaseReason: the record's reason, unless the pause reason says it.
const releaseReason = computed(() => {
  const r = (release.value?.reason || '').trim()
  return r && !(ap.value?.pausedReason || '').includes(r) ? r : ''
})
const pipelineURL = computed(() =>
  kind.value === 'release' ? release.value?.pipelineURL : implTask.value?.status.ci?.pipelineURL,
)
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

// act: an action button, 44 px high on phones.
const act = { color: 'neutral', variant: 'outline', size: 'sm', class: 'min-h-11 justify-center tablet:min-h-0' } as const
// select: the skip select, 16 px and 44 px high on phones.
const select =
  'min-h-11 max-w-full min-w-0 rounded-(--radius-sm) border border-(--border-strong) bg-(--surface) px-2 font-[inherit] text-base text-(--text) tablet:h-[34px] tablet:min-h-0 tablet:max-w-[28ch] tablet:text-[13px]'
// stack: a row of controls, a column on phones.
const stack = 'flex min-w-0 flex-col items-stretch gap-1.5 tablet:flex-row tablet:flex-wrap tablet:items-center tablet:gap-2'
const wrap = '[overflow-wrap:anywhere]'
</script>

<template>
  <section
    v-if="ap"
    class="mt-3 flex min-w-0 flex-col gap-2 rounded-(--radius) border border-l-3 border-(--border) border-l-(--tone-warning-fg) bg-(--surface) px-4 py-3.5 shadow-(--shadow) [&>p]:m-0"
    data-pause-card
  >
    <h2 class="m-0">{{ pauseTitle(kind) }}</h2>
    <Markdown v-if="ap.pausedReason" :class="wrap" :text="ap.pausedReason" />
    <p v-else-if="!kind" class="muted">Waiting for a human.</p>
    <p v-if="pauseExplain(kind)" class="muted">{{ pauseExplain(kind) }}</p>

    <div v-if="release" class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
      <span>Final release</span>
      <code v-if="release.releasedSHA || release.sha" class="faint">
        {{ (release.releasedSHA || release.sha || '').slice(0, 7) }}
      </code>
      <span v-if="release.issues?.length" class="muted" :class="wrap">
        contains {{ release.issues.map((n) => `#${n}`).join(', ') }}
      </span>
    </div>
    <Markdown v-if="releaseReason" :class="wrap" :text="releaseReason" />

    <div v-if="issue" class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
      <ExtLink :href="issueURL">#{{ issue }}</ExtLink>
      <span v-if="issueTitle" :class="wrap">{{ issueTitle }}</span>
      <ExtLink v-if="item?.mergeRequestURL" :href="item.mergeRequestURL">
        {{ item.mergeRequestIID ? `!${item.mergeRequestIID}` : 'MR' }}
      </ExtLink>
      <code v-if="head" class="faint">{{ head.slice(0, 7) }}</code>
      <CompanionChip v-if="companion" :companion="companion" />
    </div>

    <ul v-if="limitLines.length" class="m-0 pl-[18px]">
      <li v-for="l in limitLines" :key="l">{{ l }}</li>
    </ul>
    <p v-if="endsBy && !exits.length" class="faint">{{ endsBy }}</p>
    <div v-if="ap.waitingFor" class="rounded-(--radius-sm) bg-(--tone-info-bg) px-3 py-2 text-(--tone-info-fg)">
      A run is working on #{{ ap.waitingFor }} now.
    </div>
    <p v-if="override" class="muted">
      Merge anyway was given by {{ override.by }} <RelTime :time="override.at" />.
    </p>

    <div v-if="exits.length" data-way-out>
      <h3 class="my-1 text-sm">Way out</h3>
      <ul v-if="way.auto.length" class="m-0 mb-2 flex list-none flex-col gap-1.5 p-0">
        <li v-for="e in way.auto" :key="e.id" class="flex min-w-0 flex-col gap-0.5">
          <span class="flex min-w-0 flex-wrap items-center gap-2">
            <span :class="wrap">{{ e.when }}</span>
            <Badge :tone="e.tone">{{ e.chip }}</Badge>
          </span>
          <span v-if="e.detail" class="muted" :class="wrap">{{ e.detail }}</span>
          <span class="faint">→ {{ e.does }}</span>
        </li>
      </ul>
      <ul v-if="way.remedies.length" class="m-0 mb-2 flex list-none flex-col gap-2.5 p-0 tablet:gap-1.5">
        <li v-for="r in remedies" :key="r.id" class="flex min-w-0 flex-col gap-0.5">
          <span :class="stack">
            <span v-if="r.shown?.type === 'skipSelect'" :class="stack">
              <select v-model="skipIssue" :class="select" aria-label="Issue to skip" :disabled="!!sending">
                <option value="">issue…</option>
                <option v-for="it in skipOptions" :key="it.iid" :value="it.iid">#{{ it.iid }} {{ it.title }}</option>
              </select>
              <UButton
                v-bind="act"
                :disabled="!!sending || !skipIssue"
                :label="sending === r.shown.action.id ? '…' : r.shown.action.label"
                @click="skipPicked(r.shown.action)"
              />
            </span>
            <UButton
              v-else-if="r.shown"
              v-bind="act"
              :disabled="!!sending || (r.shown.action.confirm && !!confirmHead)"
              :label="sending === r.shown.action.id ? '…' : r.shown.action.label"
              @click="run(r.shown.action)"
            />
            <code v-else-if="r.elsewhere" :class="wrap">{{ agentCommand(r.command) }}</code>
            <span v-if="r.shown || !r.elsewhere" :class="wrap">{{ r.text }}</span>
            <span v-else :class="wrap">{{ r.elsewhere }}</span>
            <ExtLink v-if="r.href" class="min-h-11 tablet:min-h-0" :href="r.href">open</ExtLink>
          </span>
          <span class="faint">→ {{ r.does }}</span>
        </li>
      </ul>
    </div>

    <PromptCard v-if="prompt" :namespace="namespace" :name="name" :prompt="prompt" />

    <div v-else-if="shown.length" class="mt-1" :class="stack" data-pause-actions>
      <template v-for="s in shown" :key="s.action.id">
        <UButton
          v-if="s.type === 'task'"
          v-bind="act"
          :to="{ name: 'task', params: { namespace, name, task: s.task } }"
          :label="s.action.label"
        />
        <UButton
          v-else-if="s.type === 'ext' && safeURL(s.href)"
          v-bind="act"
          :to="safeURL(s.href)"
          target="_blank"
          :label="s.action.label"
        />
        <span v-else-if="s.type === 'skipSelect'" :class="stack">
          <select v-model="skipIssue" :class="select" aria-label="Issue to skip" :disabled="!!sending">
            <option value="">issue…</option>
            <option v-for="it in skipOptions" :key="it.iid" :value="it.iid">#{{ it.iid }} {{ it.title }}</option>
          </select>
          <UButton
            v-bind="act"
            :disabled="!!sending || !skipIssue"
            :label="sending === s.action.id ? '…' : s.action.label"
            @click="skipPicked(s.action)"
          />
        </span>
        <UButton
          v-else-if="s.type === 'command'"
          v-bind="act"
          :disabled="!!sending || (s.action.confirm && !!confirmHead)"
          :label="sending === s.action.id ? '…' : s.action.label"
          @click="run(s.action)"
        />
      </template>
    </div>

    <div
      v-if="mergeAction && confirmHead"
      class="flex flex-col gap-2 rounded-(--radius-sm) bg-(--tone-warning-bg) px-3 py-2 text-(--tone-warning-fg) tablet:flex-row tablet:flex-wrap tablet:items-center"
      data-confirm
    >
      <span :class="wrap">
        Merge <code>{{ confirmHead.slice(0, 7) }}</code> without a full review? CI, MR state and approvals still apply.
      </span>
      <span :class="stack">
        <UButton
          v-bind="act"
          color="primary"
          variant="solid"
          :disabled="!!sending"
          :label="sending === mergeAction.id ? '…' : 'Confirm'"
          @click="confirmMerge(mergeAction)"
        />
        <UButton v-bind="act" :disabled="!!sending" label="Cancel" @click="confirmHead = ''" />
      </span>
    </div>

    <p v-if="sent" :class="wrap" role="status">
      <strong>{{ sent.label }}:</strong>
      <span v-if="answer.state === 'pending'" class="faint"> sent…</span>
      <span v-else-if="answer.state === 'failed'" class="text-(--tone-danger-fg)"> {{ answer.reason }}</span>
      <span v-else-if="answer.redacted" class="muted"> answered</span>
      <span v-else class="whitespace-pre-wrap"> {{ answer.text }}</span>
    </p>
    <p v-if="postError" class="text-(--tone-danger-fg)" role="alert">{{ postError }}</p>
    <p v-if="commandsHidden && !prompt" class="faint">
      Commands need Desk chat, or <code>/agent …</code> on the
      <ExtLink v-if="milestone.tracking" :href="milestone.tracking.webURL">tracking issue</ExtLink>
      <template v-else>tracking issue</template>.
    </p>
  </section>
</template>
