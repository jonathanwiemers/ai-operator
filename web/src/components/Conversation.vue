<script setup lang="ts">
import { errorMessage } from '../format'
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { disconnectGitLab, getGitLabStatus, getMilestoneThread, getTaskThread, LoginRequiredError, postMilestoneNote, postTaskNote } from '../client'
import { keepThroughSignIn } from '../signInDraft'
import ExtLink from './ExtLink.vue'
import NoteThread from './NoteThread.vue'
import { fromGitLabNotes } from '../noteThread'
import { noteNotice } from '../noteNotice'
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
keepThroughSignIn(() => `comment:${props.namespace}/${props.name}/${props.task ?? ''}/${props.milestone ?? ''}`, body)
const sending = ref(false)
const sendError = ref('')
// sent: what became of Desk commands the server sent instead of posting.
const sent = ref('')

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
    if (!(e instanceof LoginRequiredError)) error.value = errorMessage(e)
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
    if (status.value?.connected) error.value = errorMessage(e)
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
  sent.value = ''
  try {
    // A refusal (400) keeps the text and shows under the box.
    sent.value = noteNotice(await postNote(body.value))
    body.value = ''
    await loadThread()
  } catch (e) {
    sendError.value = errorMessage(e)
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
    <UAlert v-if="gitlabError" :color="alertColor('warning')" variant="subtle" class="mt-3">
      <template #description>Connecting GitLab did not work: {{ gitlabError }}. Try again.</template>
    </UAlert>
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <div v-if="status && !status.connected && status.login" class="px-4 py-3.5">
      <p class="mt-0 mb-2.5">Connect your GitLab account to see the comments here and write comments, in your own name.</p>
      <UButton :href="connectURL" color="primary" label="Connect GitLab" />
    </div>
    <div v-else-if="status && !status.connected" class="empty">GitLab is not set up for your user here.</div>
    <template v-else-if="status?.connected">
      <div class="flex flex-wrap items-center gap-2 border-b border-(--border) px-3 py-2 text-[13px]">
        <UButton
          v-for="p in parts"
          :key="p.target"
          color="neutral"
          :variant="p.target === active ? 'outline' : 'ghost'"
          size="sm"
          :class="p.target === active ? 'bg-(--surface-2) text-(--text)' : 'text-(--text-2)'"
          :label="label(p)"
          :aria-pressed="p.target === active"
          @click="active = p.target"
        />
        <span class="flex-1" />
        <ExtLink v-if="current" :href="current.webURL">Open in GitLab</ExtLink>
        <span class="faint">@{{ status.username }}</span>
        <UButton v-if="!status.fixed" color="neutral" variant="link" size="sm" label="Disconnect" @click="disconnect" />
      </div>
      <div class="max-h-[520px] overflow-y-auto">
        <div v-if="current && current.notes === null" class="empty">These comments could not be read with your account.</div>
        <UEmpty v-else-if="!notes.length" icon="i-lucide-message-square" title="No comments yet." variant="naked" size="sm" />
        <NoteThread v-else :notes="threadNotes" />
      </div>
      <form
        v-if="steer && current"
        class="grid gap-2 border-t border-(--border) bg-(--surface-2) px-3.5 py-3"
        @submit.prevent="send"
      >
        <div class="flex flex-wrap gap-1.5">
          <UButton
            v-for="c in commands"
            :key="c"
            color="neutral"
            variant="outline"
            size="xs"
            class="mono rounded-full text-(--text-2)"
            :label="c.trim()"
            @click="insert(c)"
          />
        </div>
        <p v-if="chat" class="faint m-0 text-[13px]">
          Messages go to the pilot on {{ label(current) }} as <code>/pilot …</code>, in your name. Only the milestone
          owners' instructions count; post <code>/agent</code> commands in GitLab.
        </p>
        <UTextarea
          v-model="body"
          :rows="3"
          autoresize
          :maxrows="12"
          class="w-full"
          :aria-label="chat ? 'Message to the pilot' : 'Comment'"
          :placeholder="
            chat
              ? `Message the pilot as @${status.username}, e.g. why are we paused?`
              : `Comment on ${label(current)} as @${status.username}. Start with /agent to start a run.`
          "
          @keydown.meta.enter="send"
          @keydown.ctrl.enter="send"
        />
        <div class="flex items-center gap-2">
          <span v-if="sendError" role="alert" class="text-[13px] text-(--tone-danger-fg)">{{ sendError }}</span>
          <span v-else-if="sent" role="status" class="text-[13px] text-(--text-2)">{{ sent }}</span>
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
