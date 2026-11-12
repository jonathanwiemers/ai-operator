<script setup lang="ts">
// Budgets (docs/ui.md, "Costs page"): the budgets of milestones whose
// autopilot is not done, the milestone's own and its issues', as used. Shown
// only, never set or enforced. Live state, not bound to the range. The
// server sorts by share used; unknown spend is shown as unknown, never $0.
import { computed, ref } from 'vue'
import UButton from '@nuxt/ui/components/Button.vue'
import { RouterLink } from 'vue-router'
import type { BudgetRowView } from '../generated/views'
import { cost } from '../format'

const props = defineProps<{ budgets: BudgetRowView[] }>()

const FIRST = 10
const all = ref(false)
const rows = computed(() => (all.value ? props.budgets : props.budgets.slice(0, FIRST)))

// Spend is "" while the operator cannot read it.
const known = (b: BudgetRowView) => b.spent !== ''
const pct = (b: BudgetRowView) => (known(b) ? Math.min(100, Math.max(0, Math.round(b.share * 100))) : 0)
const warn = (b: BudgetRowView) => known(b) && b.share >= 0.8
const title = (b: BudgetRowView) => b.title || (b.issueIID ? `Issue #${b.issueIID}` : `Milestone #${b.milestoneIID}`)
const kind = (b: BudgetRowView) => (b.issueIID ? `Issue #${b.issueIID}` : 'Milestone')
const amount = (b: BudgetRowView) => `${known(b) ? cost(b.spent) : 'unknown'} / ${cost(b.limit)}`

function to(b: BudgetRowView) {
  const params = { namespace: b.namespace, name: b.project, iid: String(b.milestoneIID) }
  return b.issueIID ? { name: 'issue' as const, params: { ...params, issue: String(b.issueIID) } } : { name: 'stream-milestone' as const, params }
}
const key = (b: BudgetRowView) => `${b.namespace}/${b.project}/${b.milestoneIID}/${b.issueIID}`
</script>

<template>
  <section class="section" aria-labelledby="costs-budgets" data-costs-budgets>
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="costs-budgets">Budgets</h2>
      <span class="faint">Live state, not bound to the range; shown only, set on the milestone</span>
    </div>
    <div v-if="!budgets.length" class="card empty" data-budgets-empty>No unfinished milestone has a budget.</div>
    <template v-else>
      <ul class="card m-0 flex list-none flex-col divide-y divide-(--border) p-0">
        <li v-for="b in rows" :key="key(b)" class="flex min-w-0 flex-col gap-1.5 px-3.5 py-3" :data-budget="key(b)" :data-warn="warn(b) || undefined">
          <div class="flex min-w-0 flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
            <RouterLink :to="to(b)" class="min-w-0 text-(--accent) underline-offset-2 hover:underline [overflow-wrap:anywhere]">{{ title(b) }}</RouterLink>
            <span class="whitespace-nowrap text-[13px] tabular-nums" :class="{ italic: !known(b) }">{{ amount(b) }}</span>
          </div>
          <span
            v-if="known(b)"
            class="block h-1.5 w-full overflow-hidden rounded-full bg-(--surface-3)"
            role="progressbar"
            :aria-valuenow="pct(b)"
            aria-valuemin="0"
            aria-valuemax="100"
            :aria-label="`${pct(b)}% of the budget used`"
          >
            <span class="block h-full" :class="warn(b) ? 'bg-(--tone-warning-fg)' : 'bg-(--tone-progress-fg)'" :style="{ width: `${pct(b)}%` }" />
          </span>
          <div class="faint text-[13px] [overflow-wrap:anywhere]">
            {{ kind(b) }} · {{ b.namespace }}/{{ b.project }}<template v-if="known(b)"> · {{ pct(b) }}% used</template>
            <template v-else> · spend unknown</template>
            <template v-if="warn(b)"> · at 80 % or more</template>
          </div>
        </li>
      </ul>
      <div v-if="budgets.length > FIRST && !all" class="mt-2.5 text-center">
        <UButton color="neutral" variant="outline" :label="`Show all ${budgets.length}`" data-budgets-all @click="all = true" />
      </div>
    </template>
  </section>
</template>
