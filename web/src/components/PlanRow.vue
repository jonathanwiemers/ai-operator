<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import ExtLink from './ExtLink.vue'
import ProgressBar from './ProgressBar.vue'
import RelTime from './RelTime.vue'
import { stateTone } from '../milestoneRows'
import { planMilestoneLink, planPhaseTone, planSpendLabel, planTitle } from '../planRows'
import type { PlanListItem } from '../generated/views'

// One plan row (decision 10): project (only when the list spans projects),
// title linking to the plan, phase, spend of budget, last activity, and for
// a created milestone its %N link with state and progress, or "Not planned"
// while the Desk does not track it. Flex rows that wrap on phones.
const props = defineProps<{ item: PlanListItem; showProject?: boolean }>()

const title = computed(() => planTitle(props.item.title, props.item.createdAt))
const link = computed(() => planMilestoneLink(props.item))
const to = computed(() => ({
  name: 'plans' as const,
  params: { namespace: props.item.namespace, name: props.item.project, plan: props.item.name },
}))
const project = computed(() => ({
  name: 'stream-project' as const,
  params: { namespace: props.item.namespace, name: props.item.project },
}))
</script>

<template>
  <li class="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-t border-(--border) px-3.5 py-[9px] text-[13px] first:border-t-0" data-plan-row>
    <RouterLink
      v-if="showProject"
      class="basis-full truncate text-xs font-semibold text-(--text-3)"
      :to="project"
      :title="`${item.namespace}/${item.project}`"
    >
      {{ item.project }}
    </RouterLink>
    <RouterLink class="row-link min-w-0 flex-1 font-semibold [overflow-wrap:anywhere]" :to="to">{{ title }}</RouterLink>
    <Badge :tone="planPhaseTone(item.phase)">{{ item.phase }}</Badge>
    <span class="flex basis-full flex-wrap items-center gap-x-3 gap-y-1.5 text-(--text-2)">
      <span class="whitespace-nowrap tabular-nums" title="Spend of budget">{{ planSpendLabel(item.spendUSD, item.budgetUSD) }}</span>
      <span class="whitespace-nowrap text-(--text-3)">active <RelTime :time="item.lastActivityAt" /></span>
      <span v-if="link" class="flex min-w-0 flex-[1_1_14rem] flex-wrap items-center gap-x-2 gap-y-1" data-plan-milestone>
        <RouterLink v-if="'to' in link" class="font-semibold" :to="link.to">{{ link.label }}</RouterLink>
        <template v-else>
          <ExtLink :href="link.href">{{ link.label }}</ExtLink>
          <span class="text-xs text-(--text-3)">{{ link.note }}</span>
        </template>
        <template v-if="item.milestone">
          <Badge :tone="stateTone(item.milestone.state)">{{ item.milestone.state }}</Badge>
          <ProgressBar :merged="item.milestone.progress.merged" :total="item.milestone.progress.total" :label="title" />
        </template>
      </span>
    </span>
  </li>
</template>
