<script setup lang="ts">
import { computed } from 'vue'

// A section heading of a result; level 3 is a subsection. base is the
// heading level of a level 2 block (a research report's outline, clamped at
// h6); without it the block is a bold paragraph, as Markdown headings are.
// The classes keep the look whatever the element.
const props = defineProps<{ text: string; level?: number; base?: number }>()

const tag = computed(() => (props.base ? `h${Math.min(props.base + (props.level === 3 ? 1 : 0), 6)}` : 'p'))
</script>

<template>
  <component
    :is="tag"
    v-if="level === 3"
    class="mt-2 mb-0 text-[15px] leading-[1.25] font-[650] tracking-normal text-(--text) normal-case [overflow-wrap:anywhere]"
  >
    {{ text }}
  </component>
  <component :is="tag" v-else class="mt-2 mb-0 text-lg font-[650] tracking-normal text-inherit normal-case [overflow-wrap:anywhere]">
    {{ text }}
  </component>
</template>
