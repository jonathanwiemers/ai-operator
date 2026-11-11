<script setup lang="ts">
import { h, type Component, type FunctionalComponent } from 'vue'
import type { Block } from '../../types'
import Markdown from '../Markdown.vue'
import HeadingBlock from './HeadingBlock.vue'
import ItemsBlock from './ItemsBlock.vue'
import TableBlock from './TableBlock.vue'
import StatsBlock from './StatsBlock.vue'
import LinksBlock from './LinksBlock.vue'

// The blocks of a research result, one component per type. Unknown types
// (from a newer schema) are skipped; the server drops them already.
defineProps<{ blocks: Block[] }>()

// A markdown block is a document: the doc variant's spacing and headings.
const MarkdownBlock: FunctionalComponent<{ text: string }> = (p) => h(Markdown, { text: p.text, variant: 'doc' })
MarkdownBlock.props = ['text']

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
  <div class="flex min-w-0 flex-col gap-4">
    <template v-for="(b, i) in blocks" :key="i">
      <component :is="components[b.type]" v-if="Object.hasOwn(components, b.type)" v-bind="fields(b)" />
    </template>
  </div>
</template>
