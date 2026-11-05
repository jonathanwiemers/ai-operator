<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { usePromptAnswer } from '../promptAnswer'
import {
  canAnswerText,
  promptAnchor,
  promptOpen,
  promptOptions,
  promptStatus,
  readOnlyText,
  textBox,
} from '../promptCard'
import type { PromptView } from '../types'
import Badge from './Badge.vue'
import RelTime from './RelTime.vue'
import MarkdownBody from './stream/MarkdownBody.vue'

// PromptCard is one question to the owners in full: the question, its
// context, the options (recommended first, a detail line each), a text box
// when it takes free text, and who answered it. One click on an option, or
// the text box, sends an answer command to the prompt's target task; the
// operator's reply shows under the card. The question, context, options and
// answers are untrusted: text, or markdown without HTML.
const props = defineProps<{ namespace: string; name: string; prompt: PromptView }>()

const anchor = computed(() => promptAnchor(props.prompt.id))
const open = computed(() => promptOpen(props.prompt))
const options = computed(() => promptOptions(props.prompt))
const status = computed(() => promptStatus(props.prompt))
const readOnly = computed(() => readOnlyText(props.prompt))
const withText = computed(() => canAnswerText(props.prompt))
const box = computed(() => textBox(props.prompt))
const clickable = computed(() => open.value && props.prompt.canAnswer)

const { sending, error, sent, answer, busy, send } = usePromptAnswer(
  () => ({ namespace: props.namespace, name: props.name }),
  () => props.prompt,
)

const text = ref('')
async function sendText() {
  if (await send({ text: text.value })) text.value = ''
}

// A Desk link from GitLab ends in #prompt-<id>: scroll to the card once it
// is there.
const route = useRoute()
const el = ref<HTMLElement | null>(null)
function scrollIfLinked() {
  if (route.hash === `#${anchor.value}`) void nextTick(() => el.value?.scrollIntoView({ block: 'start' }))
}
onMounted(scrollIfLinked)
watch(() => route.hash, scrollIfLinked)
</script>

<template>
  <section :id="anchor" ref="el" class="card prompt" :class="{ closed: !open }">
    <div class="row head">
      <code>{{ prompt.id }}</code>
      <Badge :tone="status.tone">{{ prompt.state || 'open' }}</Badge>
      <span v-if="prompt.issueIID" class="faint">#{{ prompt.issueIID }}</span>
      <span v-if="prompt.mrIID" class="faint">!{{ prompt.mrIID }}</span>
      <span class="spacer" />
      <span class="faint"><RelTime :time="prompt.postedAt" /></span>
    </div>
    <p class="question">{{ prompt.question }}</p>
    <MarkdownBody v-if="prompt.context" class="context" :text="prompt.context" />

    <ul class="options">
      <li v-for="o in options" :key="o.id" :class="{ chosen: o.chosen, rec: o.recommended }">
        <button
          v-if="clickable"
          type="button"
          class="opt"
          :class="{ primary: o.recommended }"
          :disabled="busy"
          @click="send({ option: o.id })"
        >
          <span class="label">{{ sending === o.id ? '…' : o.label }}</span>
        </button>
        <span v-else class="opt static">
          <span v-if="o.chosen" aria-label="chosen">✓ </span><span class="label">{{ o.label }}</span>
        </span>
        <span v-for="t in o.tags" :key="t" class="tag">{{ t }}</span>
        <p v-if="o.detail" class="detail muted">{{ o.detail }}</p>
        <p v-for="(a, i) in o.actions" :key="i" class="action faint">→ {{ a }}</p>
      </li>
    </ul>

    <form v-if="withText" class="box" @submit.prevent="sendText">
      <textarea
        v-model="text"
        rows="2"
        :placeholder="box.placeholder"
        :aria-label="`Answer ${prompt.id} in your words`"
        :disabled="busy"
      />
      <div class="row">
        <span class="hint faint">{{ box.hint }}</span>
        <span class="spacer" />
        <button type="submit" class="btn" :disabled="busy || !text.trim()">
          {{ sending === 'text' ? '…' : 'Send' }}
        </button>
      </div>
    </form>

    <p v-if="readOnly" class="faint">{{ readOnly }}</p>
    <p class="status" :class="{ muted: open }">
      {{ status.text }}<template v-if="!open && status.at"> · <RelTime :time="status.at" /></template>
    </p>
    <MarkdownBody v-if="prompt.answer?.text && prompt.state === 'answered'" class="said" :text="prompt.answer.text" />
    <MarkdownBody v-if="prompt.answer?.outcome" class="outcome" :text="prompt.answer.outcome" />

    <div v-if="sent" class="reply">
      <strong>{{ sent.label }}:</strong>
      <span v-if="answer.state === 'pending'" class="faint"> sent…</span>
      <span v-else-if="answer.state === 'failed'" class="err"> {{ answer.reason }}</span>
      <span v-else-if="answer.redacted" class="muted"> answered</span>
      <MarkdownBody v-else :text="answer.text" />
    </div>
    <p v-if="error" class="err">{{ error }}</p>
  </section>
</template>

<style scoped>
.prompt {
  padding: 12px 14px;
  border-left: 3px solid var(--tone-warning-fg);
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  scroll-margin-top: 12px;
}
.prompt.closed {
  border-left-color: var(--border-strong);
}
.prompt p {
  margin: 0;
}
.head {
  gap: 8px;
  font-size: 12.5px;
}
.question {
  font-weight: 600;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.context {
  max-height: 220px;
  overflow-y: auto;
  font-size: 13px;
  padding: 6px 8px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
}
.options {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 8px;
}
.options li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 6px;
  min-width: 0;
}
.opt {
  font: inherit;
  font-weight: 550;
  text-align: left;
  padding: 6px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-strong);
  background: var(--surface);
  color: var(--text);
  cursor: pointer;
  max-width: 100%;
  overflow-wrap: anywhere;
}
.opt:hover:not(:disabled) {
  background: var(--surface-2);
}
.opt.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
.opt:disabled {
  opacity: 0.5;
  cursor: default;
}
.opt.static {
  cursor: default;
  font-weight: 500;
}
.chosen .opt.static {
  font-weight: 650;
  border-color: var(--tone-success-fg);
}
.tag {
  font-size: 11.5px;
  color: var(--text-3);
}
.tag + .tag::before {
  content: '· ';
}
.detail,
.action {
  flex-basis: 100%;
  font-size: 12.5px;
  overflow-wrap: anywhere;
}
.box {
  display: grid;
  gap: 6px;
}
textarea {
  width: 100%;
  box-sizing: border-box;
  font: inherit;
  padding: 6px 8px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  resize: vertical;
}
textarea:disabled,
.btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.hint {
  font-size: 12px;
  flex: 1 1 160px;
}
.status {
  font-size: 13px;
}
.said,
.outcome {
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
