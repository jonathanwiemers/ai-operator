<script setup lang="ts">
import { computed } from 'vue'
import TaskCard from './TaskCard.vue'
import type { Stage, Task } from '../types'

// The issues of a milestone in columns like the GitLab board.
const props = defineProps<{ tasks: Task[] }>()

const columns: { key: string; title: string; stages: Stage[] }[] = [
  { key: 'progress', title: 'In progress', stages: ['working', 'waiting', 'pending'] },
  { key: 'attention', title: 'Needs you', stages: ['needs-input', 'failed', 'blocked'] },
  { key: 'ci', title: 'CI', stages: ['ci'] },
  { key: 'review', title: 'Review', stages: ['review', 'planned'] },
]

const filled = computed(() =>
  columns.map((c) => ({ ...c, tasks: props.tasks.filter((t) => c.stages.includes(t.stage)) })),
)
</script>

<template>
  <div class="board">
    <section v-for="c in filled" :key="c.key" class="column" :class="[c.key, { empty: !c.tasks.length }]">
      <header>
        <span>{{ c.title }}</span>
        <span class="count">{{ c.tasks.length }}</span>
      </header>
      <div class="cards">
        <TaskCard v-for="t in c.tasks" :key="t.name" :task="t" />
        <div v-if="!c.tasks.length" class="none">—</div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.board {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 12px;
}

@media (max-width: 1100px) {
  .board {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .board {
    grid-template-columns: minmax(0, 1fr);
  }
  /* One column per row: empty ones only take space. */
  .column.empty {
    display: none;
  }
}

.column {
  background: var(--surface-2);
  border-radius: var(--radius);
  padding: 10px;
  min-height: 80px;
  border-top: 3px solid var(--border-strong);
}

.column.progress {
  border-top-color: var(--tone-progress-fg);
}

.column.attention {
  border-top-color: var(--tone-warning-fg);
}

.column.ci {
  border-top-color: var(--tone-info-fg);
}

.column.review {
  border-top-color: var(--tone-success-fg);
}

header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 12px;
  font-weight: 650;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-2);
  padding: 2px 4px 10px;
}

.count {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 999px;
  padding: 0 8px;
  font-size: 11px;
}

.cards {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.none {
  color: var(--text-3);
  text-align: center;
  padding: 12px 0;
}
</style>
