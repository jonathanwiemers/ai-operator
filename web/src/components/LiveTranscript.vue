<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import Badge from './Badge.vue'
import TranscriptLine from './TranscriptLine.vue'
import { tokenCount } from '../format'
import { foldName, foldTranscript } from '../transcriptFold'
import type { TranscriptEntry } from '../types'

// LiveTranscript follows the task's pod: the runner's steps and the agent's
// session as they happen. A new pod (the next run after a pause) opens a new
// stream. Pods are named like their task, so run marks a new run in the
// same-named pod.
const props = defineProps<{ namespace: string; name: string; task: string; pod?: string; run?: string }>()

// maxEntries keeps a long run from growing the page without bound.
const maxEntries = 3000

const entries = ref<TranscriptEntry[]>([])
const state = ref<'idle' | 'live' | 'ended' | 'error'>('idle')
const endReason = ref('')
const follow = ref(true)
const list = ref<HTMLElement | null>(null)
let source: EventSource | null = null

function close() {
  source?.close()
  source = null
}

function open() {
  close()
  entries.value = []
  endReason.value = ''
  if (!props.pod) {
    state.value = 'idle'
    return
  }
  const path = `/api/v1/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/tasks/${encodeURIComponent(props.task)}/transcript`
  const es = new EventSource(path)
  source = es
  state.value = 'live'
  es.addEventListener('entry', (ev) => {
    const e = JSON.parse((ev as MessageEvent).data) as TranscriptEntry
    entries.value.push(e)
    if (entries.value.length > maxEntries) entries.value.splice(0, entries.value.length - maxEntries)
    if (follow.value) void nextTick(() => list.value?.scrollTo({ top: list.value.scrollHeight }))
  })
  es.addEventListener('end', (ev) => {
    const d = JSON.parse((ev as MessageEvent).data) as { reason?: string }
    endReason.value = d.reason ?? ''
    state.value = 'ended'
    close()
  })
  es.addEventListener('error', () => {
    // The server ends the stream with an end event; anything else is an
    // error (no access, the UI restarting). No automatic reconnect.
    if (source === es) {
      state.value = 'error'
      close()
    }
  })
}

watch(() => `${props.pod ?? ''}#${props.run ?? ''}`, open, { immediate: true })
onUnmounted(close)

function onScroll() {
  const el = list.value
  if (el) follow.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40
}

const time = (t?: string) => (t ? new Date(t).toLocaleTimeString() : '')
// rows fold each subagent's lines under the tool line that started it.
const rows = computed(() => foldTranscript(entries.value))
const runs = computed(() => new Set(entries.value.map((e) => e.run).filter(Boolean)).size)
const endText = computed(() => {
  switch (endReason.value) {
    case 'no pod':
      return 'No pod is running for this task. The run log of the last run is on the merge request.'
    case 'pod stopped':
      return 'The pod has stopped.'
    case 'session expired':
      return 'Your login expired; reload the page.'
    default:
      return 'The pod is not available (it is starting or has just stopped).'
  }
})
</script>

<template>
  <div class="card live">
    <div class="row bar">
      <Badge :tone="state === 'live' ? 'progress' : state === 'error' ? 'danger' : 'muted'">
        {{ state === 'live' ? 'Live' : state === 'error' ? 'Not available' : 'Stopped' }}
      </Badge>
      <span v-if="pod" class="mono faint">{{ pod }}</span>
      <span v-if="runs > 1" class="faint">{{ runs }} runs</span>
      <span class="spacer" />
      <button v-if="state !== 'live' && pod" class="btn" type="button" @click="open">Reconnect</button>
    </div>
    <div ref="list" class="entries" @scroll="onScroll">
      <div v-if="!entries.length" class="empty">
        {{ state === 'live' ? 'Waiting for the run…' : state === 'error' ? 'The transcript could not be loaded.' : endText }}
      </div>
      <template v-for="(r, i) in rows" :key="r.kind === 'fold' ? `fold-${r.tool.toolUseID}` : i">
        <TranscriptLine v-if="r.kind === 'entry'" :e="r.entry" />
        <details v-else class="fold">
          <summary class="entry">
            <span class="t faint mono">{{ time(r.tool.time) }}</span>
            <span class="who sub faint">subagent</span>
            <span>
              <strong>{{ foldName(r) }}</strong>
              <template v-if="r.model"> · <span class="mono">{{ r.model }}</span></template>
              · {{ tokenCount(r.tokens) }} tok
            </span>
            <span class="faint">{{ r.count }} {{ r.count === 1 ? 'entry' : 'entries' }}</span>
            <span v-if="r.tool.subagent && r.tool.input" class="faint input">{{ r.tool.input }}</span>
          </summary>
          <div class="children">
            <TranscriptLine v-for="(c, j) in r.children" :key="j" :e="c" />
            <div v-if="!r.children.length" class="entry faint">No events yet.</div>
          </div>
        </details>
      </template>
    </div>
    <div v-if="state === 'ended' && entries.length" class="foot faint">{{ endText }}</div>
  </div>
</template>

<style scoped>
.live {
  overflow: hidden;
}

.bar {
  gap: 10px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--border);
}

.btn {
  font: inherit;
  font-size: 13px;
  padding: 3px 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  cursor: pointer;
}

.entries {
  max-height: 560px;
  overflow-y: auto;
  padding: 8px 0;
  font-size: 13px;
}

.entry {
  display: flex;
  gap: 8px;
  align-items: baseline;
  padding: 3px 14px;
}

.t {
  flex: none;
  width: 72px;
  font-size: 12px;
}

.who {
  flex: none;
  width: 56px;
}

.who.sub {
  width: auto;
  min-width: 56px;
  color: var(--text-2);
}

.input {
  overflow-wrap: anywhere;
}

/* A subagent's fold: one row, its lines indented inside. */
.fold summary {
  cursor: pointer;
  list-style: none;
}

.fold summary::-webkit-details-marker {
  display: none;
}

.fold summary .who::before {
  content: '▸ ';
}

.fold[open] summary .who::before {
  content: '▾ ';
}

.fold .children {
  margin: 2px 14px 4px 94px;
  border-left: 2px solid var(--border);
}

.fold .children :deep(.entry) {
  padding-left: 10px;
}

.foot {
  padding: 8px 14px;
  border-top: 1px solid var(--border);
  font-size: 13px;
}
</style>
