<script setup lang="ts">
import { computed } from 'vue'
import { stageInfo, stageOrder } from '../format'
import type { Stage } from '../types'

// A stacked bar of task counts per stage, with a legend.
const props = defineProps<{ stages: Partial<Record<Stage, number>> }>()

const parts = computed(() => {
  return stageOrder
    .filter((s) => (props.stages[s] ?? 0) > 0)
    .map((s) => ({
      stage: s,
      count: props.stages[s] ?? 0,
      ...stageInfo[s],
    }))
})
</script>

<template>
  <div v-if="parts.length" class="stagebar">
    <div class="bar" role="img" :aria-label="parts.map((p) => `${p.count} ${p.label}`).join(', ')">
      <span
        v-for="p in parts"
        :key="p.stage"
        class="seg"
        :class="'tone-' + p.tone"
        :style="{ flexGrow: p.count }"
      />
    </div>
    <div class="legend">
      <span v-for="p in parts" :key="p.stage" class="item" :title="p.hint">
        <span class="swatch" :class="'tone-' + p.tone" />{{ p.count }} {{ p.label }}
      </span>
    </div>
  </div>
  <div v-else class="faint">No tasks yet</div>
</template>

<style scoped>
.bar {
  display: flex;
  height: 8px;
  border-radius: 999px;
  overflow: hidden;
  gap: 2px;
  background: var(--surface-2);
}

.seg {
  flex: 1 1 0;
  min-width: 4px;
}

.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  margin-top: 8px;
  font-size: 12px;
  color: var(--text-2);
}

.item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.swatch {
  width: 8px;
  height: 8px;
  border-radius: 2px;
}

.tone-muted {
  background: var(--tone-muted-fg);
}
.tone-info {
  background: var(--tone-info-fg);
}
.tone-progress {
  background: var(--tone-progress-fg);
}
.tone-success {
  background: var(--tone-success-fg);
}
.tone-warning {
  background: var(--tone-warning-fg);
}
.tone-danger {
  background: var(--tone-danger-fg);
}
.tone-neutral {
  background: var(--tone-neutral-fg);
}
</style>
