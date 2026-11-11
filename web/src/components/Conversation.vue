<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError, disconnectGitLab, getGitLabStatus, getMilestoneThread, getTaskThread, LoginRequiredError, postMilestoneNote, postTaskNote } from '../client'
import ExtLink from './ExtLink.vue'
import NoteThread from './NoteThread.vue'
import { fromGitLabNotes } from '../noteThread'
import type { GitLabStatus, NoteView, ThreadPart } from '../types'

// Conversation shows the task's issue, merge request and tracking issue
// comments, read with the user's own GitLab account, and posts comments in
// their name. The operator handles them like any comment written in GitLab.
// With milestone instead of task it is the milestone's chat: its tracking
// issue, and messages go to the pilot (the server adds the pilot prefix).
// stamp changes when the task does (a run, CI): then comments are likely new.
const props = defineProps<{
  namespace: string
  name: string
  task?: string
  milestone?: number
  steer: boolean
  stamp: string
}>()
const chat = computed(() => props.milestone !== undefined)

const status = ref<GitLabStatus | null>(null)
const parts = ref<ThreadPart[]>([])
const active = ref<ThreadPart['target']>('issue')
const error = ref('')
const body = ref('')
const sending = ref(false)
const sendError = ref('')

// loadParts and postNote reach the milestone's or the task's routes.
function loadParts() {
  return chat.value
    ? getMilestoneThread(props.namespace, props.name, props.milestone!)
    : getTaskThread(props.namespace, props.name, props.task ?? '')
}
function postNote(text: string) {
  return chat.value
    ? postMilestoneNote(props.namespace, props.name, props.milestone!, text)
    : postTaskNote(props.namespace, props.name, props.task ?? '', active.value, text)
}

// The connection's outcome comes back as a code in the URL: shown as a fixed
// text, then removed from the URL.
const errorTexts: Record<string, string> = {
  expired: 'the connection took too long',
  state: 'the answer did not match the request',
  refused: 'GitLab refused it',
  token: 'GitLab did not hand out a token',
}
const params = new URLSearchParams(window.location.search)
const gitlabError = params.has('gitlab_error') ? (errorTexts[params.get('gitlab_error') ?? ''] ?? 'it failed') : ''
if (params.has('gitlab_error')) {
  params.delete('gitlab_error')
  const q = params.toString()
  window.history.replaceState(window.history.state, '', window.location.pathname + (q ? `?${q}` : ''))
}
const returnTo = () => window.location.pathname + window.location.search
const connectURL = computed(() => `${status.value?.login}?return=${encodeURIComponent(returnTo())}`)

async function loadStatus() {
  try {
    status.value = await getGitLabStatus()
  } catch (e) {
    if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
  }
}

let seq = 0
async function loadThread() {
  if (!status.value?.connected) return
  const mine = ++seq
  try {
    const r = await loadParts()
    if (mine !== seq) return // a newer load is on its way
    parts.value = r.parts
    const first = parts.value[0]
    if (first && !parts.value.some((p) => p.target === active.value)) active.value = first.target
    error.value = ''
  } catch (e) {
    if (e instanceof LoginRequiredError) return
    // A revoked connection: show the connect button again.
    await loadStatus()
    if (status.value?.connected) error.value = e instanceof Error ? e.message : String(e)
  }
}

const current = computed(() => parts.value.find((p) => p.target === active.value))
const notes = computed<NoteView[]>(() => current.value?.notes ?? [])
const threadNotes = computed(() => fromGitLabNotes(notes.value, status.value?.username))
const label = (p: ThreadPart) => (p.target === 'mr' ? `MR !${p.iid}` : p.target === 'tracking' ? `Tracking #${p.iid}` : `Issue #${p.iid}`)

// Commands to start a comment with; the operator reads them like in GitLab.
const commands = computed(() =>
  chat.value
    ? ['yes P', 'no P', 'rules', 'stop', 'go']
    : active.value === 'tracking'
    ? ['/agent pause', '/agent resume', '/agent skip #']
    : ['/agent ', '/agent use claude opus', '/agent use cursor auto', '/agent effort high', '/agent retry'],
)
function insert(c: string) {
  body.value = body.value ? `${c}\n${body.value}` : c
}

async function send() {
  if (!body.value.trim() || sending.value) return
  sending.value = true
  sendError.value = ''
  try {
    await postNote(body.value)
    body.value = ''
    await loadThread()
  } catch (e) {
    sendError.value = e instanceof ApiError || e instanceof Error ? e.message : String(e)
  } finally {
    sending.value = false
  }
}

async function disconnect() {
  await disconnectGitLab().catch(() => undefined)
  parts.value = []
  await loadStatus()
}

let timer: number | undefined
onMounted(async () => {
  await loadStatus()
  await loadThread()
  // GitLab sends the UI no events: look again every 30 seconds.
  timer = window.setInterval(loadThread, 30000)
})
onUnmounted(() => window.clearInterval(timer))
// The task changed (a run, CI): new comments are likely.
watch(() => props.stamp, loadThread)
</script>

<template>
  <div class="card overflow-hidden">
    <div v-if="gitlabError" class="banner warning">Connecting GitLab did not work: {{ gitlabError }}. Try again.</div>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="status && !status.connected && status.login" class="px-4 py-3.5">
      <p class="mt-0 mb-2.5">Connect your GitLab account to see the comments here and write comments, in your own name.</p>
      <UButton :href="connectURL" color="primary" label="Connect GitLab" />
    </div>
    <div v-else-if="status && !status.connected" class="empty">GitLab is not set up for your user here.</div>
    <template v-else-if="status?.connected">
      <div class="flex flex-wrap items-center gap-2 border-b border-(--border) px-3 py-2 text-[13px]">
        <button
          v-for="p in parts"
          :key="p.target"
          type="button"
          class="cursor-pointer rounded-(--radius-sm) border px-2.5 py-1 font-[inherit]"
          :class="
            p.target === active
              ? 'border-(--border-strong) bg-(--surface-2) text-(--text)'
              : 'border-transparent bg-transparent text-(--text-2)'
          "
          :aria-pressed="p.target === active"
          @click="active = p.target"
        >
          {{ label(p) }}
        </button>
        <span class="flex-1" />
        <ExtLink v-if="current" :href="current.webURL">Open in GitLab</ExtLink>
        <span class="faint">@{{ status.username }}</span>
        <button
          v-if="!status.fixed"
          type="button"
          class="cursor-pointer border-0 bg-transparent p-0 font-[inherit] text-(--link)"
          @click="disconnect"
        >
          Disconnect
        </button>
      </div>
      <div class="max-h-[520px] overflow-y-auto">
        <div v-if="current && current.notes === null" class="empty">These comments could not be read with your account.</div>
        <div v-else-if="!notes.length" class="empty">No comments yet.</div>
        <NoteThread v-else :notes="threadNotes" />
      </div>
      <form
        v-if="steer && current"
        class="grid gap-2 border-t border-(--border) bg-(--surface-2) px-3.5 py-3"
        @submit.prevent="send"
      >
        <div class="flex flex-wrap gap-1.5">
          <button
            v-for="c in commands"
            :key="c"
            type="button"
            class="mono cursor-pointer rounded-full border border-(--border-strong) bg-(--surface) px-2 py-0.5 text-xs text-(--text-2)"
            @click="insert(c)"
          >
            {{ c.trim() }}
          </button>
        </div>
        <p v-if="chat" class="faint m-0 text-[13px]">
          Messages go to the pilot on {{ label(current) }} as <code>/pilot …</code>, in your name. Only the milestone
          owners' instructions count; post <code>/agent</code> commands in GitLab.
        </p>
        <textarea
          v-model="body"
          rows="3"
          class="box-border w-full resize-y rounded-(--radius-sm) border border-(--border-strong) px-2.5 py-2 font-[inherit]"
          :placeholder="
            chat
              ? `Message the pilot as @${status.username}, e.g. why are we paused?`
              : `Comment on ${label(current)} as @${status.username}. Start with /agent to start a run.`
          "
          @keydown.meta.enter="send"
          @keydown.ctrl.enter="send"
        />
        <div class="flex items-center gap-2">
          <span v-if="sendError" class="text-[13px] text-(--tone-danger-fg)">{{ sendError }}</span>
          <span class="flex-1" />
          <UButton
            type="submit"
            color="primary"
            :disabled="sending || !body.trim()"
            :label="sending ? 'Posting…' : chat ? 'Send to pilot' : 'Post comment'"
          />
        </div>
      </form>
    </template>
    <div v-else class="empty">Loading…</div>
  </div>
</template>
