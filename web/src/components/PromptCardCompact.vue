<script setup lang="ts">
import { computed } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { usePromptAnswer } from '../promptAnswer'
import { promptOpen, promptStatus, readOnlyText, recommendedOption } from '../promptCard'
import type { PromptView } from '../types'

// PromptCardCompact is a prompt's short form for the stream and the Inbox:
// the question, the recommended option as a button and "more…" to the full
// card. The question and option label are untrusted text: rendered as text.
const props = defineProps<{ namespace: string; name: string; prompt: PromptView; to: RouteLocationRaw }>()

const open = computed(() => promptOpen(props.prompt))
const recommended = computed(() => recommendedOption(props.prompt))
const status = computed(() => promptStatus(props.prompt))
const readOnly = computed(() => readOnlyText(props.prompt))

const { sending, error, sent, answer, busy, send } = usePromptAnswer(
  () => ({ namespace: props.namespace, name: props.name }),
  () => props.prompt,
)
</script>

<template>
  <section class="card prompt-compact" :class="{ closed: !open }">
    <div class="head">
      <code>{{ prompt.id }}</code>
      <strong class="question" :title="prompt.question">{{ prompt.question }}</strong>
    </div>
    <div class="row">
      <button
        v-if="open && prompt.canAnswer && recommended"
        type="button"
        class="btn primary"
        :disabled="busy"
        @click="send({ option: recommended.id })"
      >
        {{ sending === recommended.id ? '…' : recommended.label }}
      </button>
      <span v-else-if="!open" class="muted line">{{ status.text }}</span>
      <span v-else-if="readOnly" class="faint line">{{ readOnly }}</span>
      <span class="spacer" />
      <RouterLink :to="to" class="more">more…</RouterLink>
    </div>
    <p v-if="sent" class="reply">
      <strong>{{ sent.label }}:</strong>
      <span v-if="answer.state === 'pending'" class="faint"> sent…</span>
      <span v-else-if="answer.state === 'failed'" class="err"> {{ answer.reason }}</span>
      <span v-else class="muted"> answered</span>
    </p>
    <p v-if="error" class="err">{{ error }}</p>
  </section>
</template>

<style scoped>
.prompt-compact {
  padding: 10px 14px;
  border-left: 3px solid var(--tone-warning-fg);
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.prompt-compact.closed {
  border-left-color: var(--border-strong);
}
.prompt-compact p {
  margin: 0;
}
.head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}
.question,
.line {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.line {
  font-size: 13px;
  flex: 1 1 0;
}
.btn {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.more {
  white-space: nowrap;
  font-size: 13px;
}
.reply {
  font-size: 13px;
  overflow-wrap: anywhere;
}
.err {
  color: var(--tone-danger-fg);
}
</style>
