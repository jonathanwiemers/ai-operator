<script setup lang="ts">
import Badge from '../Badge.vue'
import ExtLink from '../ExtLink.vue'
import RelTime from '../RelTime.vue'
import type { ResearchItem } from '../../types'

// News or findings. hideTopic: the items are grouped by topic already.
defineProps<{ items: ResearchItem[]; hideTopic?: boolean }>()

const importance: Record<number, { label: string; tone: 'warning' | 'muted' } | undefined> = {
  3: { label: 'important', tone: 'warning' },
  1: { label: 'minor', tone: 'muted' },
}
</script>

<template>
  <ul class="items">
    <li v-for="(it, i) in items" :key="i" class="item" :class="{ high: it.importance === 3 }">
      <div class="row title-row">
        <ExtLink :href="it.url" nofollow class="title">{{ it.title }}</ExtLink>
        <Badge v-if="it.new" tone="info">new</Badge>
        <Badge v-if="importance[it.importance]" :tone="importance[it.importance]!.tone">
          {{ importance[it.importance]!.label }}
        </Badge>
      </div>
      <p v-if="it.summary" class="summary prose">{{ it.summary }}</p>
      <div class="meta faint">
        <span v-if="it.source">{{ it.source }}</span>
        <RelTime v-if="it.publishedAt" :time="it.publishedAt" />
        <span v-if="it.topic && !hideTopic">{{ it.topic }}</span>
      </div>
    </li>
  </ul>
</template>

<style scoped>
.items {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
  background: var(--border);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}

.item {
  background: var(--surface);
  padding: 12px 16px;
  border-left: 3px solid transparent;
}

.item.high {
  border-left-color: var(--accent);
}

.title-row {
  align-items: baseline;
}

.title {
  font-weight: 600;
  font-size: 15px;
  overflow-wrap: anywhere;
}

.summary {
  margin: 4px 0 0;
  color: var(--text-2);
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin-top: 4px;
  font-size: 12px;
}

.meta:empty {
  display: none;
}
</style>
