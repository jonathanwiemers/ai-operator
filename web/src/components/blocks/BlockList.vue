<script setup lang="ts">
import type { Component } from 'vue'
import type { Block } from '../../types'
import HeadingBlock from './HeadingBlock.vue'
import MarkdownBlock from './MarkdownBlock.vue'
import ItemsBlock from './ItemsBlock.vue'
import TableBlock from './TableBlock.vue'
import StatsBlock from './StatsBlock.vue'
import LinksBlock from './LinksBlock.vue'

// The blocks of a research result, one component per type. Unknown types
// (from a newer schema) are skipped; the server drops them already.
defineProps<{ blocks: Block[] }>()

const components: Record<string, Component> = {
  heading: HeadingBlock,
  markdown: MarkdownBlock,
  items: ItemsBlock,
  table: TableBlock,
  stats: StatsBlock,
  links: LinksBlock,
}

// The block's fields are the component's props.
function fields(b: Block): Record<string, unknown> {
  const { type: _, ...rest } = b
  return rest
}
</script>

<template>
  <div class="blocks">
    <template v-for="(b, i) in blocks" :key="i">
      <component :is="components[b.type]" v-if="Object.hasOwn(components, b.type)" v-bind="fields(b)" />
    </template>
  </div>
</template>

<style scoped>
.blocks {
  display: flex;
  flex-direction: column;
  gap: 16px;
  min-width: 0;
}
</style>
