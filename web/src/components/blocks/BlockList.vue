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
// headingLevel: the level of the result's top headings (HeadingBlock's base,
// a markdown block's "#"); without it they are bold paragraphs.
const props = defineProps<{ blocks: Block[]; headingLevel?: number }>()

// A markdown block is a document: the doc variant's spacing and headings.
const MarkdownBlock: FunctionalComponent<{ text: string; headingLevel?: number }> = (p) =>
  h(Markdown, { text: p.text, variant: 'doc', headingLevel: p.headingLevel })
MarkdownBlock.props = ['text', 'headingLevel']

const components: Record<string, Component> = {
  heading: HeadingBlock,
  markdown: MarkdownBlock,
  items: ItemsBlock,
  table: TableBlock,
  stats: StatsBlock,
  links: LinksBlock,
}

// The block's fields are the component's props, with the heading level.
function fields(b: Block): Record<string, unknown> {
  const { type: _, ...rest } = b
  if (b.type === 'heading') return { ...rest, base: props.headingLevel }
  if (b.type === 'markdown') return { ...rest, headingLevel: props.headingLevel }
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
