<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import StageBadge from './StageBadge.vue'
import RelTime from './RelTime.vue'
import { ciLabel, ciTone, cost } from '../format'
import type { Task } from '../types'

const props = defineProps<{ task: Task }>()
const run = computed(() => props.task.status.run ?? {})
const to = computed(() => ({
  name: 'task',
  params: { namespace: props.task.namespace, name: props.task.project, task: props.task.name },
}))
</script>

<template>
  <RouterLink :to="to" class="task card">
    <div class="head">
      <span v-if="task.issue" class="iid">#{{ task.issue.iid }}</span>
      <span v-else class="iid">Plan</span>
      <span class="spacer" />
      <StageBadge :stage="task.stage" />
    </div>
    <div class="title">{{ task.issue?.title || task.milestone?.title || task.name }}</div>
    <div v-if="run.currentStep" class="step">
      <span class="spinner" aria-hidden="true" />
      <span>{{ run.currentStep }}</span>
    </div>
    <div class="foot">
      <Badge v-if="task.status.ci" :tone="ciTone(task.status.ci.state)">CI: {{ ciLabel[task.status.ci.state] }}</Badge>
      <Badge v-if="run.mergeRequestIID" tone="neutral">!{{ run.mergeRequestIID }}</Badge>
      <Badge v-if="(run.blockingFindings ?? 0) > 0" tone="danger">{{ run.blockingFindings }} findings</Badge>
      <Badge v-if="task.status.phase === 'Waiting' && run.limitedEngine" tone="warning">{{ run.limitedEngine }} limit</Badge>
      <span class="spacer" />
      <span class="meta">{{ cost(run.costUSD) }} · <RelTime :time="run.lastActivityTime ?? task.createdAt" /></span>
    </div>
  </RouterLink>
</template>

<style scoped>
.task {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  color: inherit;
  transition:
    box-shadow 0.15s,
    border-color 0.15s;
}

.task:hover {
  text-decoration: none;
  border-color: var(--border-strong);
  box-shadow: var(--shadow-hover);
}

.head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.iid {
  font-family: var(--mono);
  font-size: 12px;
  color: var(--text-3);
}

.spacer {
  flex: 1;
}

.title {
  font-weight: 600;
  line-height: 1.35;
  overflow-wrap: anywhere;
}

.step {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12.5px;
  color: var(--tone-progress-fg);
}

.spinner {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 2px solid currentColor;
  border-right-color: transparent;
  animation: spin 0.9s linear infinite;
  flex: none;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .spinner {
    animation: none;
  }
}

.foot {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.meta {
  font-size: 12px;
  color: var(--text-3);
}
</style>
