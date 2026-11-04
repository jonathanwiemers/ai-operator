<script setup lang="ts">
import Badge from './Badge.vue'
import type { TranscriptEntry } from '../types'

// TranscriptLine is one entry of the live transcript, on the main thread
// or inside a subagent's fold.
defineProps<{ e: TranscriptEntry }>()

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
      <span class="text">{{ e.text }}</span>
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
      <h4 v-if="e.system">System prompt</h4>
      <pre v-if="e.system">{{ e.system }}</pre>
      <h4>Prompt</h4>
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

.prompt h4 {
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
