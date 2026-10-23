<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError, get, post, LoginRequiredError } from '../api'
import { live } from '../live'
import ExtLink from './ExtLink.vue'
import RelTime from './RelTime.vue'
import type { GitLabStatus, NoteView, ThreadPart } from '../types'

// Conversation shows the task's issue, merge request and tracking issue
// comments, read with the user's own GitLab account, and posts comments in
// their name. The operator handles them like any comment written in GitLab.
const props = defineProps<{ namespace: string; name: string; task: string; steer: boolean }>()

const status = ref<GitLabStatus | null>(null)
const parts = ref<ThreadPart[]>([])
const active = ref<ThreadPart['target']>('issue')
const error = ref('')
const body = ref('')
const sending = ref(false)
const sendError = ref('')

const base = computed(
  () => `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/tasks/${encodeURIComponent(props.task)}`,
)
const returnTo = () => window.location.pathname + window.location.search
const connectURL = computed(() => `${status.value?.login ?? '/oauth2/gitlab/login'}?return=${encodeURIComponent(returnTo())}`)
const gitlabError = new URLSearchParams(window.location.search).get('gitlab_error')

async function loadStatus() {
  try {
    status.value = await get<GitLabStatus>('/gitlab')
  } catch (e) {
    if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
  }
}

async function loadThread() {
  if (!status.value?.connected) return
  try {
    const r = await get<{ parts: ThreadPart[] }>(`${base.value}/thread`)
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
  active.value === 'tracking'
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
    await post(`${base.value}/notes`, { target: active.value, body: body.value })
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
// The task changed (a run, a label): new comments are likely.
watch(() => live.version, loadThread)
</script>

<template>
  <div class="card conv">
    <div v-if="gitlabError" class="banner warning">Connecting GitLab did not work ({{ gitlabError }}); try again.</div>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="status && !status.connected" class="body">
      <p>Connect your GitLab account to see the comments here and write comments, in your own name.</p>
      <a class="btn primary" :href="connectURL">Connect GitLab</a>
    </div>
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
        <button type="button" class="link" @click="disconnect">Disconnect</button>
      </div>
      <div class="notes">
        <div v-if="current && current.notes === null" class="empty">These comments could not be read with your account.</div>
        <div v-else-if="!notes.length" class="empty">No comments yet.</div>
        <div v-for="n in notes" :key="n.id" class="note" :class="{ system: n.system }">
          <div class="meta">
            <strong>@{{ n.author }}</strong>
            <span class="faint"><RelTime :time="n.createdAt" /></span>
          </div>
          <div class="text">{{ n.body }}</div>
        </div>
      </div>
      <form v-if="steer && current" class="composer" @submit.prevent="send">
        <div class="row chips">
          <button v-for="c in commands" :key="c" type="button" class="chip mono" @click="insert(c)">{{ c.trim() }}</button>
        </div>
        <textarea
          v-model="body"
          rows="3"
          :placeholder="`Comment on ${label(current)} as @${status.username}. Start with /agent to start a run.`"
          @keydown.meta.enter="send"
          @keydown.ctrl.enter="send"
        />
        <div class="row">
          <span v-if="sendError" class="err">{{ sendError }}</span>
          <span class="spacer" />
          <button type="submit" class="btn primary" :disabled="sending || !body.trim()">
            {{ sending ? 'Posting…' : 'Post comment' }}
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
  white-space: pre-wrap;
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
