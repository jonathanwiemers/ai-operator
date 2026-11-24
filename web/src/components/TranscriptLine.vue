<script setup lang="ts">
import Markdown from './Markdown.vue'
import Badge from './Badge.vue'
import { computed } from 'vue'
import { ownerMessages } from '../transcriptFold'
import type { TranscriptEntry } from '../types'

// TranscriptLine is one entry of the live transcript, on the main thread
// or inside a subagent's fold. A user turn that carries owner messages
// shows each with its owner, without the runner's closing line.
const props = defineProps<{ e: TranscriptEntry }>()
const owners = computed(() => (props.e.kind === 'user' ? ownerMessages(props.e.text) : []))

const stepTone = (s?: string) => (s === 'done' ? 'success' : s === 'failed' ? 'danger' : s === 'deferred' ? 'warning' : 'progress')
const time = (t?: string) => (t ? new Date(t).toLocaleTimeString() : '')
</script>

<template>
  <div class="entry" :class="e.kind">
    <span class="t faint mono">{{ time(e.time) }}</span>
    <template v-if="e.kind === 'step'">
      <Badge :tone="stepTone(e.status)">{{ e.status }}</Badge>
      <span class="step">{{ e.stepNo }} {{ e.step }}</span>
      <span v-if="e.result" class="faint">{{ e.result }}</span>
    </template>
    <template v-else-if="e.kind === 'cli'">
      <Badge tone="info">{{ e.role || 'agent' }}</Badge>
      <span class="mono">{{ e.engine }}/{{ e.model || 'default' }}</span>
      <span v-if="e.readOnly" class="faint">read-only</span>
    </template>
    <template v-else-if="e.kind === 'thinks'">
      <span class="who faint">thinks</span>
      <span class="text thinking">{{ e.text }}</span>
    </template>
    <template v-else-if="e.kind === 'says'">
      <span class="who">says</span>
      <Markdown class="text says" :text="e.text ?? ''" />
    </template>
    <template v-else-if="e.kind === 'user' && owners.length">
      <!-- An owner's words the pilot passed on to the running call. -->
      <span class="who">user</span>
      <div class="owner-turn" data-owner-message>
        <p v-for="(m, i) in owners" :key="i">
          <Badge tone="info">Message from @{{ m.owner }} via the pilot</Badge>
          <span class="text user-text">{{ m.text }}</span>
        </p>
      </div>
    </template>
    <template v-else-if="e.kind === 'user'">
      <!-- A message written into the running call, which the agent took in. -->
      <span class="who">user</span>
      <span class="text user-text">{{ e.text }}</span>
    </template>
    <template v-else-if="e.kind === 'tool'">
      <span class="who faint">tool</span>
      <code class="tool">{{ e.tool }}</code>
      <span class="mono input">{{ e.input }}</span>
    </template>
    <template v-else-if="e.kind === 'answers'">
      <span class="who">answers</span>
      <span class="faint">the answer is posted on GitLab</span>
    </template>
    <details v-else-if="e.kind === 'prompt'" class="prompt">
      <summary>
        Prompt to <strong>{{ e.role || 'agent' }}</strong>
        <span class="mono faint">{{ e.engine }}/{{ e.model || 'default' }}</span>
      </summary>
      <p v-if="e.system" class="prompt-label">System prompt</p>
      <pre v-if="e.system">{{ e.system }}</pre>
      <p class="prompt-label">Prompt</p>
      <pre>{{ e.prompt }}</pre>
    </details>
  </div>
</template>

<style scoped>
.entry {
  display: flex;
  gap: 8px;
  align-items: baseline;
  padding: 3px 14px;
}

.entry.step {
  margin-top: 4px;
}

.t {
  flex: none;
  width: 72px;
  font-size: 12px;
}

.who {
  flex: none;
  width: 56px;
  color: var(--text-2);
}

.text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.says {
  white-space: normal;
}

.user-text {
  font-weight: 500;
}

.owner-turn {
  display: grid;
  gap: 4px;
  min-width: 0;
  padding: 4px 8px;
  border-left: 2px solid var(--ui-primary);
}

.owner-turn p {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: baseline;
  margin: 0;
}

.thinking {
  color: var(--text-3);
  font-style: italic;
}

.tool {
  padding: 0 5px;
  border-radius: 4px;
  background: var(--surface-2);
}

.input {
  overflow-wrap: anywhere;
  color: var(--text-2);
}

.prompt {
  flex: 1;
  min-width: 0;
}

.prompt summary {
  cursor: pointer;
}

.prompt .prompt-label {
  margin: 10px 0 4px;
  font-size: 12px;
  color: var(--text-3);
}

.prompt pre {
  max-height: 360px;
  overflow: auto;
  margin: 0;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: 12px;
}
</style>
