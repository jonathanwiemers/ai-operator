<script setup lang="ts">
import Markdown from '../Markdown.vue'
import Badge from '../Badge.vue'
import ExtLink from '../ExtLink.vue'
import RelTime from '../RelTime.vue'
import type { ResearchItem } from '../../types'

// News or findings, one under the other. hideTopic: the items are grouped by
// topic already. The title wraps, its badges follow it; the title link is at
// least 44 px high on a phone. The summary is block markdown.
defineProps<{ items: ResearchItem[]; hideTopic?: boolean }>()

const importance: Record<number, { label: string; tone: 'warning' | 'muted' } | undefined> = {
  3: { label: 'important', tone: 'warning' },
  1: { label: 'minor', tone: 'muted' },
}
</script>

<template>
  <ul class="m-0 flex list-none flex-col gap-px overflow-hidden rounded-(--radius) border border-(--border) bg-(--border) p-0" data-block="items">
    <li
      v-for="(it, i) in items"
      :key="i"
      class="min-w-0 border-s-3 bg-(--surface) px-4 py-3"
      :class="it.importance === 3 ? 'border-s-(--accent)' : 'border-s-transparent'"
    >
      <div class="flex flex-wrap items-center gap-x-2 gap-y-1 tablet:items-baseline">
        <ExtLink :href="it.url" nofollow class="min-h-11 text-[15px] font-semibold [overflow-wrap:anywhere] tablet:min-h-0">{{ it.title }}</ExtLink>
        <Badge v-if="it.new" tone="info">new</Badge>
        <Badge v-if="importance[it.importance]" :tone="importance[it.importance]!.tone">
          {{ importance[it.importance]!.label }}
        </Badge>
      </div>
      <Markdown v-if="it.summary" class="mt-1 text-(--text-2)" :text="it.summary" />
      <div v-if="it.source || it.publishedAt || (it.topic && !hideTopic)" class="mt-1 flex flex-wrap gap-x-3.5 gap-y-1 text-xs text-(--text-3)">
        <span v-if="it.source" class="[overflow-wrap:anywhere]">{{ it.source }}</span>
        <RelTime v-if="it.publishedAt" :time="it.publishedAt" />
        <span v-if="it.topic && !hideTopic">{{ it.topic }}</span>
      </div>
    </li>
  </ul>
</template>
