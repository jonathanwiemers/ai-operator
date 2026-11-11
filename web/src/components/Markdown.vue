<script setup lang="ts">
import { h } from 'vue'
import { renderInline, renderMarkdown } from '../markdown'
import ExtLink from './ExtLink.vue'

// Agent and human text rendered by markdown.ts: no HTML string, links only
// to http(s) URLs. block renders paragraphs, lists, code and tables; inline
// renders one line for rows that are links already (links show as their
// label). variant picks the block spacing: note for chat, events and prompts,
// doc for research results and retrospectives.
const props = withDefaults(defineProps<{ text: string; mode?: 'block' | 'inline'; variant?: 'note' | 'doc' }>(), {
  mode: 'block',
  variant: 'note',
})

const Rendered = () =>
  props.mode === 'inline'
    ? renderInline(props.text)
    : renderMarkdown(props.text, (href, label) => h(ExtLink, { href, nofollow: true }, () => label))
</script>

<template>
  <span v-if="mode === 'inline'" class="md-inline"><Rendered /></span>
  <div v-else class="md" :class="variant === 'doc' ? 'md-doc' : 'md-note'"><Rendered /></div>
</template>

<style scoped>
.md {
  overflow-wrap: anywhere;
}

/* note */
.md-note :deep(p) {
  margin: 0 0 6px;
}
.md-note :deep(p:last-child) {
  margin-bottom: 0;
}
.md-note :deep(pre) {
  margin: 8px 0;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  overflow-x: auto;
}
.md-note :deep(code) {
  font-family: var(--mono);
  font-size: 12.5px;
}
.md-note :deep(ul),
.md-note :deep(ol) {
  margin: 6px 0;
  padding-left: 1.2em;
}

/* doc */
.md-doc {
  line-height: 1.6;
}

.md-doc :deep(p),
.md-doc :deep(ul),
.md-doc :deep(ol),
.md-doc :deep(pre),
.md-doc :deep(blockquote) {
  margin: 0 0 10px;
}

.md-doc > :deep(:last-child) {
  margin-bottom: 0;
}

.md-doc :deep(li > p) {
  margin: 0;
}

.md-doc :deep(ul),
.md-doc :deep(ol) {
  padding-left: 22px;
}

.md-doc :deep(h3),
.md-doc :deep(h4),
.md-doc :deep(h5) {
  margin: 14px 0 6px;
  font-size: 15px;
  text-transform: none;
  letter-spacing: normal;
  color: var(--text);
}

.md-doc :deep(h4) {
  font-size: 14px;
}

.md-doc :deep(h5) {
  font-size: 13px;
  color: var(--text-2);
}

.md-doc :deep(blockquote) {
  padding-left: 12px;
  border-left: 3px solid var(--border-strong);
  color: var(--text-2);
}

.md-doc :deep(code) {
  padding: 1px 4px;
  border-radius: 4px;
  background: var(--surface-2);
}

.md-doc :deep(pre) {
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  overflow-x: auto;
}

.md-doc :deep(pre code) {
  padding: 0;
  background: none;
}

.md-doc :deep(.ext) {
  display: inline;
}
</style>
