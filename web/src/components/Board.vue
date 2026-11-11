<script setup lang="ts">
import { computed } from 'vue'
import TaskCard from './TaskCard.vue'
import type { Stage, Task } from '../types'

// The issues of a milestone in columns like the GitLab board.
//
// Utilities only (docs/ui.md, "Board"): four columns on desktop, two on a
// tablet, stacked on a phone. Every column keeps its heading and count; on
// a phone an empty one is only its heading.
const props = defineProps<{ tasks: Task[] }>()

const columns: { key: string; title: string; stages: Stage[]; accent: string }[] = [
  { key: 'progress', title: 'In progress', stages: ['working', 'waiting', 'pending'], accent: 'border-t-(--tone-progress-fg)' },
  { key: 'attention', title: 'Needs you', stages: ['needs-input', 'failed', 'blocked'], accent: 'border-t-(--tone-warning-fg)' },
  { key: 'ci', title: 'CI', stages: ['ci'], accent: 'border-t-(--tone-info-fg)' },
  { key: 'review', title: 'Review', stages: ['review', 'planned'], accent: 'border-t-(--tone-success-fg)' },
]

const filled = computed(() =>
  columns.map((c) => ({ ...c, tasks: props.tasks.filter((t) => c.stages.includes(t.stage)) })),
)
</script>

<template>
  <div class="grid grid-cols-1 gap-3 tablet:grid-cols-2 desktop:grid-cols-4" data-board>
    <section
      v-for="c in filled"
      :key="c.key"
      class="min-w-0 rounded-(--radius) border-t-3 bg-(--surface-2) p-2.5"
      :class="[c.accent, c.tasks.length ? 'min-h-20' : 'tablet:min-h-20']"
      :aria-label="c.title"
      :data-column="c.key"
    >
      <header
        class="flex items-center justify-between px-1 pt-0.5 text-xs font-[650] tracking-[0.04em] text-(--text-2) uppercase"
        :class="c.tasks.length ? 'pb-2.5' : 'pb-0.5 tablet:pb-2.5'"
      >
        <span>{{ c.title }}</span>
        <span class="rounded-full border border-(--border) bg-(--surface) px-2 text-[11px] tabular-nums" data-count>
          {{ c.tasks.length }}
        </span>
      </header>
      <div class="flex flex-col gap-2">
        <TaskCard v-for="t in c.tasks" :key="t.name" :task="t" />
        <div v-if="!c.tasks.length" class="hidden py-3 text-center text-(--text-3) tablet:block">—</div>
      </div>
    </section>
  </div>
</template>
