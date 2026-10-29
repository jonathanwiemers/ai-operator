<script setup lang="ts">
import { h } from 'vue'
import { renderMarkdown } from '../../markdown'
import ExtLink from '../ExtLink.vue'

// Markdown text of a result, rendered to VNodes by markdown.ts: no HTML
// string, links only to http(s) URLs.
const props = defineProps<{ text: string }>()

const Rendered = () => renderMarkdown(props.text, (href, label) => h(ExtLink, { href, nofollow: true }, () => label))
</script>

<template>
  <div class="md"><Rendered /></div>
</template>

<style scoped>
.md {
  overflow-wrap: anywhere;
  line-height: 1.6;
}

.md :deep(p),
.md :deep(ul),
.md :deep(ol),
.md :deep(pre),
.md :deep(blockquote) {
  margin: 0 0 10px;
}

.md > :deep(:last-child) {
  margin-bottom: 0;
}

.md :deep(li > p) {
  margin: 0;
}

.md :deep(ul),
.md :deep(ol) {
  padding-left: 22px;
}

.md :deep(h3),
.md :deep(h4),
.md :deep(h5) {
  margin: 14px 0 6px;
  font-size: 15px;
  text-transform: none;
  letter-spacing: normal;
  color: var(--text);
}

.md :deep(h4) {
  font-size: 14px;
}

.md :deep(h5) {
  font-size: 13px;
  color: var(--text-2);
}

.md :deep(blockquote) {
  padding-left: 12px;
  border-left: 3px solid var(--border-strong);
  color: var(--text-2);
}

.md :deep(code) {
  padding: 1px 4px;
  border-radius: 4px;
  background: var(--surface-2);
}

.md :deep(pre) {
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  overflow-x: auto;
}

.md :deep(pre code) {
  padding: 0;
  background: none;
}

.md :deep(.ext) {
  display: inline;
}
</style>
