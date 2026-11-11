<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import CompanionChip from './CompanionChip.vue'
import StageBadge from './StageBadge.vue'
import RelTime from './RelTime.vue'
import { ciLabel, ciTone, cost, deferringEngine } from '../format'
import type { Task } from '../types'

// A task on the board (Board.vue). Utilities only (docs/ui.md, "Board"):
// the title wraps, the badges wrap onto further lines, and the whole card
// is the link, well above 44 px high.
const props = defineProps<{ task: Task }>()
const run = computed(() => props.task.status.run ?? {})
const to = computed(() => ({
  name: 'task',
  params: { namespace: props.task.namespace, name: props.task.project, task: props.task.name },
}))
</script>

<template>
  <RouterLink
    :to="to"
    class="flex min-h-11 min-w-0 flex-col rounded-(--radius) border border-(--border) bg-(--surface) shadow-(--shadow) gap-2 p-3 text-inherit transition-[box-shadow,border-color] duration-150 hover:border-(--border-strong) hover:no-underline hover:shadow-(--shadow-hover)"
    data-task-card
  >
    <div class="flex items-center gap-2">
      <span class="font-mono text-xs whitespace-nowrap text-(--text-3)">{{ task.issue ? `#${task.issue.iid}` : 'Plan' }}</span>
      <StageBadge class="ml-auto" :stage="task.stage" />
    </div>
    <div class="leading-[1.35] font-semibold [overflow-wrap:anywhere]" data-title>
      {{ task.issue?.title || task.milestone?.title || task.name }}
    </div>
    <div v-if="run.currentStep" class="flex items-center gap-2 text-[12.5px] text-(--tone-progress-fg)">
      <span
        class="size-2.5 flex-none animate-spin rounded-full border-2 border-current border-r-transparent motion-reduce:animate-none"
        aria-hidden="true"
      />
      <span class="min-w-0 [overflow-wrap:anywhere]">{{ run.currentStep }}</span>
    </div>
    <div class="flex flex-wrap items-center gap-1.5">
      <Badge v-if="task.suspend" tone="warning">Suspended</Badge>
      <!-- While a run works on the task, the CI result is the last push's. -->
      <Badge v-if="task.status.ci && task.runPending" tone="muted" :title="`CI of the last push: ${ciLabel[task.status.ci.state]}`">
        last push: {{ ciLabel[task.status.ci.state] }}
      </Badge>
      <Badge v-else-if="task.status.ci" :tone="ciTone(task.status.ci.state)">CI: {{ ciLabel[task.status.ci.state] }}</Badge>
      <Badge v-if="run.mergeRequestIID" tone="neutral">!{{ run.mergeRequestIID }}</Badge>
      <!-- The card is a link itself: the task page and the milestone panel link the companion MR. -->
      <CompanionChip v-if="task.companion" :companion="task.companion" plain />
      <Badge v-if="(run.blockingFindings ?? 0) > 0" tone="danger">{{ run.blockingFindings }} findings</Badge>
      <Badge v-if="task.status.phase === 'Waiting' && deferringEngine(run)" tone="warning">{{ deferringEngine(run) }} limit</Badge>
      <span class="ml-auto text-xs whitespace-nowrap text-(--text-3)">{{ cost(run.costUSD) }} · <RelTime :time="run.lastActivityTime ?? task.createdAt" /></span>
    </div>
  </RouterLink>
</template>
