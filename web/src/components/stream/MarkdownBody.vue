<script setup lang="ts">
import { h } from 'vue'
import { renderMarkdown } from '../../markdown'
import ExtLink from '../ExtLink.vue'

const props = defineProps<{ text: string }>()
const Rendered = () =>
  renderMarkdown(props.text, (href, label) => h(ExtLink, { href, nofollow: true }, () => label))
</script>

<template>
  <div class="md"><Rendered /></div>
</template>

<style scoped>
.md {
  overflow-wrap: anywhere;
}
.md :deep(p) {
  margin: 0 0 6px;
}
.md :deep(p:last-child) {
  margin-bottom: 0;
}
.md :deep(pre) {
  margin: 8px 0;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface-2);
  overflow-x: auto;
}
.md :deep(code) {
  font-family: var(--mono);
  font-size: 12.5px;
}
.md :deep(ul),
.md :deep(ol) {
  margin: 6px 0;
  padding-left: 1.2em;
}
</style>
