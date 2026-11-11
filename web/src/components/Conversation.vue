<script setup lang="ts">
import Markdown from './Markdown.vue'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError, get, post, LoginRequiredError } from '../api'
import ExtLink from './ExtLink.vue'
import RelTime from './RelTime.vue'
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

const base = computed(
  () =>
    `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/` +
    (chat.value ? `milestones/${props.milestone}` : `tasks/${encodeURIComponent(props.task ?? '')}`),
)
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
    status.value = await get<GitLabStatus>('/gitlab')
  } catch (e) {
    if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
  }
}

let seq = 0
async function loadThread() {
  if (!status.value?.connected) return
  const mine = ++seq
  try {
    const r = await get<{ parts: ThreadPart[] }>(`${base.value}/thread`)
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
    await post(`${base.value}/notes`, chat.value ? { body: body.value } : { target: active.value, body: body.value })
    body.value = ''
    await loadThread()
  } catch (e) {
    sendError.value = e instanceof ApiError || e instanceof Error ? e.message : String(e)
  } finally {
    sending.value = false
  }
}

async function disconnect() {
  await post('/oauth2/gitlab/logout', {}).catch(() => undefined)
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
  <div class="card conv">
    <div v-if="gitlabError" class="banner warning">Connecting GitLab did not work: {{ gitlabError }}. Try again.</div>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="status && !status.connected && status.login" class="body">
      <p>Connect your GitLab account to see the comments here and write comments, in your own name.</p>
      <a class="btn primary" :href="connectURL">Connect GitLab</a>
    </div>
    <div v-else-if="status && !status.connected" class="empty">GitLab is not set up for your user here.</div>
    <template v-else-if="status?.connected">
      <div class="row bar">
        <button
          v-for="p in parts"
          :key="p.target"
          type="button"
          class="tab"
          :class="{ on: p.target === active }"
          @click="active = p.target"
        >
          {{ label(p) }}
        </button>
        <span class="spacer" />
        <ExtLink v-if="current" :href="current.webURL">Open in GitLab</ExtLink>
        <span class="faint">@{{ status.username }}</span>
        <button v-if="!status.fixed" type="button" class="link" @click="disconnect">Disconnect</button>
      </div>
      <div class="notes">
        <div v-if="current && current.notes === null" class="empty">These comments could not be read with your account.</div>
        <div v-else-if="!notes.length" class="empty">No comments yet.</div>
        <div v-for="n in notes" :key="n.id" class="note" :class="{ system: n.system }">
          <div class="meta">
            <strong>@{{ n.author }}</strong>
            <span class="faint"><RelTime :time="n.createdAt" /></span>
          </div>
          <Markdown class="text" :text="n.body" />
        </div>
      </div>
      <form v-if="steer && current" class="composer" @submit.prevent="send">
        <div class="row chips">
          <button v-for="c in commands" :key="c" type="button" class="chip mono" @click="insert(c)">{{ c.trim() }}</button>
        </div>
        <p v-if="chat" class="hint faint">
          Messages go to the pilot on {{ label(current) }} as <code>/pilot …</code>, in your name. Only the milestone
          owners' instructions count; post <code>/agent</code> commands in GitLab.
        </p>
        <textarea
          v-model="body"
          rows="3"
          :placeholder="
            chat
              ? `Message the pilot as @${status.username}, e.g. why are we paused?`
              : `Comment on ${label(current)} as @${status.username}. Start with /agent to start a run.`
          "
          @keydown.meta.enter="send"
          @keydown.ctrl.enter="send"
        />
        <div class="row">
          <span v-if="sendError" class="err">{{ sendError }}</span>
          <span class="spacer" />
          <button type="submit" class="btn primary" :disabled="sending || !body.trim()">
            {{ sending ? 'Posting…' : chat ? 'Send to pilot' : 'Post comment' }}
          </button>
        </div>
      </form>
    </template>
    <div v-else class="empty">Loading…</div>
  </div>
</template>

<style scoped>
.conv {
  overflow: hidden;
}

.body {
  padding: 14px 16px;
}

.body p {
  margin: 0 0 10px;
}

.bar {
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border);
  flex-wrap: wrap;
}

.tab,
.chip,
.link,
.btn {
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.tab {
  padding: 4px 10px;
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--text-2);
}

.tab.on {
  border-color: var(--border-strong);
  background: var(--surface-2);
  color: var(--text);
}

.link {
  border: none;
  background: none;
  color: var(--link);
  padding: 0;
}

.btn {
  display: inline-block;
  padding: 5px 12px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  text-decoration: none;
}

.btn.primary {
  border-color: var(--brand-ink);
  background: var(--brand-ink);
  color: #fff;
}

.btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.notes {
  max-height: 520px;
  overflow-y: auto;
}

.note {
  padding: 10px 14px;
  border-bottom: 1px solid var(--border);
}

.note.system {
  opacity: 0.6;
  font-size: 13px;
}

.meta {
  display: flex;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 4px;
}

.text {
  overflow-wrap: anywhere;
  font-size: 14px;
}

.composer {
  display: grid;
  gap: 8px;
  padding: 12px 14px;
  border-top: 1px solid var(--border);
  background: var(--surface-2);
}

.hint {
  margin: 0;
  font-size: 13px;
}

.chips {
  gap: 6px;
  flex-wrap: wrap;
}

.chip {
  padding: 2px 8px;
  border: 1px solid var(--border-strong);
  border-radius: 999px;
  background: var(--surface);
  color: var(--text-2);
  font-size: 12px;
}

textarea {
  width: 100%;
  box-sizing: border-box;
  padding: 8px 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  font: inherit;
  resize: vertical;
}

.err {
  color: var(--tone-danger-fg);
  font-size: 13px;
}
</style>
