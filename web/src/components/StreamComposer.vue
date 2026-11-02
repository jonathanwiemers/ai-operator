<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError, get, post, LoginRequiredError } from '../api'
import type { GitLabStatus, ProjectDetail, Task } from '../types'

export type ComposerTarget =
  | { type: 'pilot'; milestoneIID: number; title: string; trackingIssueIID?: number }
  | { type: 'task'; task: string; issueIID: number; title: string }

const props = defineProps<{
  namespace: string
  name: string
  project: ProjectDetail | null
  // When set, prefer this milestone's pilot as default target.
  milestoneIID?: number
  steer?: boolean
}>()
const emit = defineEmits<{ posted: [] }>()

const status = ref<GitLabStatus | null>(null)
const body = ref('')
const sending = ref(false)
const sendError = ref('')
const targetOpen = ref(false)
const slashOpen = ref(false)
const target = ref<ComposerTarget | null>(null)

const slashSuggestions = [
  { cmd: '/pilot pause', hint: 'stop starting and merging' },
  { cmd: '/pilot resume', hint: 'resume starting and merging' },
  { cmd: '/pilot skip #', hint: 'skip a planned issue' },
  { cmd: '/agent use claude opus high', hint: 'model for the target issue' },
  { cmd: '/agent effort high', hint: 'effort for the next run' },
  { cmd: '/agent revisit ', hint: 'change a recorded decision' },
  { cmd: 'yes P', hint: "accept the pilot's proposal" },
]

const milestones = computed(() => props.project?.milestones ?? [])
const tasks = computed((): Task[] => {
  const p = props.project
  if (!p) return []
  return [...p.milestones.flatMap((m) => m.tasks), ...p.unplanned].filter((t) => t.issue)
})

const targets = computed((): ComposerTarget[] => {
  const out: ComposerTarget[] = []
  const ms = milestones.value
  const prefer = props.milestoneIID
  const ordered = prefer
    ? [...ms.filter((m) => m.iid === prefer), ...ms.filter((m) => m.iid !== prefer)]
    : ms
  for (const m of ordered) {
    if (!m.trackingIssueIID && !m.spec) continue
    // Active autopilot milestones, or the scoped one.
    const ap = m.spec?.status.autopilot
    const active = prefer === m.iid || ap?.phase === 'Running' || ap?.phase === 'Paused'
    if (!active && prefer !== m.iid) continue
    out.push({
      type: 'pilot',
      milestoneIID: m.iid,
      title: m.title,
      trackingIssueIID: m.trackingIssueIID,
    })
  }
  for (const t of tasks.value) {
    if (!t.issue) continue
    out.push({ type: 'task', task: t.name, issueIID: t.issue.iid, title: t.issue.title })
  }
  return out
})

const disabledReason = computed(() => {
  if (!status.value) return 'Loading…'
  if (!status.value.connected && status.value.login) return 'Connect GitLab to post.'
  if (!status.value.connected) return 'GitLab is not set up for your user here.'
  if (props.steer === false) return 'Your roles may not post comments on this project.'
  if (!targets.value.length) return 'No pilot or issue to post to yet.'
  return ''
})

const hint = computed(() => {
  const t = target.value
  if (!t) return 'Pick where to post'
  if (t.type === 'pilot') {
    const track = t.trackingIssueIID ? `tracking issue #${t.trackingIssueIID}` : `milestone %${t.milestoneIID}`
    return `Posts on ${track} as /pilot … in your GitLab name`
  }
  return `Comment on #${t.issueIID}; start with /agent to start a run`
})

const targetLabel = computed(() => {
  const t = target.value
  if (!t) return '→ Pick target'
  if (t.type === 'pilot') return `→ Pilot · ${t.title}`
  return `→ #${t.issueIID} ${t.title}`
})

const connectURL = computed(() => {
  if (!status.value?.login) return ''
  const back = window.location.pathname + window.location.search
  return `${status.value.login}?return=${encodeURIComponent(back)}`
})

watch(
  targets,
  (list) => {
    if (!list.length) {
      target.value = null
      return
    }
    if (!target.value || !list.some((t) => sameTarget(t, target.value!))) {
      target.value = list[0]!
    }
  },
  { immediate: true },
)

function sameTarget(a: ComposerTarget, b: ComposerTarget) {
  if (a.type !== b.type) return false
  if (a.type === 'pilot' && b.type === 'pilot') return a.milestoneIID === b.milestoneIID
  if (a.type === 'task' && b.type === 'task') return a.task === b.task
  return false
}

async function loadStatus() {
  try {
    status.value = await get<GitLabStatus>('/gitlab')
  } catch (e) {
    if (!(e instanceof LoginRequiredError)) sendError.value = e instanceof Error ? e.message : String(e)
  }
}

function onBodyInput() {
  slashOpen.value = /^\/\S*$/.test(body.value) || /^yes P?$/.test(body.value)
  if (slashOpen.value) targetOpen.value = false
}

function pickSlash(cmd: string) {
  body.value = cmd.endsWith(' ') || cmd.endsWith('#') ? cmd : `${cmd} `
  slashOpen.value = false
}

function pickTarget(t: ComposerTarget) {
  target.value = t
  targetOpen.value = false
}

async function send() {
  if (!body.value.trim() || sending.value || !target.value || !status.value?.connected) return
  sending.value = true
  sendError.value = ''
  try {
    const t = target.value
    if (t.type === 'pilot') {
      await post(
        `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/milestones/${t.milestoneIID}/notes`,
        { body: body.value },
      )
    } else {
      await post(
        `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/tasks/${encodeURIComponent(t.task)}/notes`,
        { target: 'issue', body: body.value },
      )
    }
    body.value = ''
    emit('posted')
  } catch (e) {
    sendError.value = e instanceof ApiError || e instanceof Error ? e.message : String(e)
  } finally {
    sending.value = false
  }
}

function onDocClick(ev: MouseEvent) {
  const el = ev.target as HTMLElement | null
  if (!el?.closest('.box')) {
    targetOpen.value = false
    slashOpen.value = false
  }
}

onMounted(() => {
  void loadStatus()
  document.addEventListener('click', onDocClick)
})
onUnmounted(() => document.removeEventListener('click', onDocClick))
</script>

<template>
  <div class="composer">
    <div v-if="disabledReason && (!status?.connected || steer === false)" class="disabled card">
      <p>{{ disabledReason }}</p>
      <a v-if="status && !status.connected && status.login" class="btn primary" :href="connectURL">Connect GitLab</a>
    </div>
    <div v-else class="box">
      <textarea
        v-model="body"
        rows="2"
        :disabled="!!disabledReason"
        :placeholder="target?.type === 'pilot' ? `Message the pilot…  /  for commands` : `Comment…  /  for commands`"
        @input="onBodyInput"
        @keydown.meta.enter="send"
        @keydown.ctrl.enter="send"
      />
      <div class="box-row">
        <button type="button" class="target" :disabled="!!disabledReason" @click.stop="targetOpen = !targetOpen">
          <span>{{ targetLabel }}</span>
          <span class="caret">▾</span>
        </button>
        <span class="hint">{{ disabledReason || hint }}</span>
        <button type="button" class="send" :disabled="sending || !body.trim() || !!disabledReason" @click="send">
          {{ sending ? '…' : 'Send' }}
        </button>
      </div>
      <div v-if="targetOpen" class="pop open" @click.stop>
        <div class="lbl">Send to</div>
        <button v-for="(t, i) in targets" :key="i" type="button" class="opt" @click="pickTarget(t)">
          <b v-if="t.type === 'pilot'">Pilot · %{{ t.milestoneIID }}</b>
          <b v-else>#{{ t.issueIID }}</b>
          <span>{{ t.type === 'pilot' ? t.title : t.title }}</span>
        </button>
      </div>
      <div v-if="slashOpen" class="pop open" @click.stop>
        <div class="lbl">Commands</div>
        <button v-for="s in slashSuggestions" :key="s.cmd" type="button" class="opt" @click="pickSlash(s.cmd)">
          <b>{{ s.cmd }}</b>
          <span>{{ s.hint }}</span>
        </button>
      </div>
    </div>
    <p v-if="sendError" class="err">{{ sendError }}</p>
  </div>
</template>

<style scoped>
.composer {
  position: sticky;
  top: 0;
  z-index: 4;
  background: var(--bg);
  padding: 14px 0 10px;
}
.disabled {
  padding: 12px 14px;
}
.disabled p {
  margin: 0 0 10px;
}
.box {
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: 12px;
  box-shadow: var(--shadow);
  position: relative;
}
.box:focus-within {
  border-color: var(--focus);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--focus) 20%, transparent);
}
textarea {
  width: 100%;
  border: 0;
  background: none;
  resize: none;
  padding: 12px 14px 4px;
  font: inherit;
  color: var(--text);
  outline: none;
  min-height: 46px;
}
textarea:disabled {
  opacity: 0.6;
}
.box-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 8px 8px 10px;
}
.target {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 4px 9px;
  border-radius: 999px;
  background: var(--pilot-bg);
  border: 1px solid var(--pilot-border);
  font-size: 12.5px;
  font-weight: 600;
  cursor: pointer;
  color: var(--text);
  font-family: inherit;
  max-width: 46%;
}
.target span:first-child {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.caret {
  opacity: 0.6;
}
.hint {
  font-size: 12px;
  color: var(--text-3);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.send {
  border: 0;
  background: var(--accent);
  color: #fff;
  font-weight: 600;
  padding: 6px 14px;
  border-radius: 8px;
  cursor: pointer;
  font: inherit;
}
.send:disabled {
  opacity: 0.5;
  cursor: default;
}
.pop {
  position: absolute;
  left: 10px;
  top: calc(100% + 6px);
  background: var(--surface);
  border: 1px solid var(--border-strong);
  border-radius: 10px;
  box-shadow: 0 8px 28px rgb(0 0 0 / 18%);
  width: min(340px, calc(100% - 20px));
  padding: 6px;
  z-index: 20;
  display: none;
}
.pop.open {
  display: block;
}
.opt {
  display: flex;
  gap: 10px;
  padding: 7px 9px;
  border-radius: 7px;
  cursor: pointer;
  align-items: baseline;
  width: 100%;
  border: 0;
  background: none;
  font: inherit;
  color: inherit;
  text-align: left;
}
.opt:hover {
  background: var(--surface-2);
}
.opt b {
  font-family: var(--mono);
  font-size: 12.5px;
  font-weight: 600;
  white-space: nowrap;
}
.opt span {
  color: var(--text-3);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.lbl {
  font-size: 11px;
  color: var(--text-3);
  padding: 6px 9px 2px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.err {
  color: var(--tone-danger-fg);
  font-size: 13px;
  margin: 6px 0 0;
}
.btn {
  display: inline-flex;
  align-items: center;
  height: 34px;
  padding: 0 14px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-strong);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-weight: 550;
  text-decoration: none;
}
.btn.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}
</style>
