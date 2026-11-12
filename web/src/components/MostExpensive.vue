<script setup lang="ts">
// Most expensive (docs/ui.md, "Costs page"): the top milestones, issues and
// runs of the range by known cost, ten each. A row names its label, project
// and, for a run, its kind; its cost is the known cost, or "n/a" / "not
// metered" when every call is unknown, never $0.00, with a mark for the
// calls of unknown cost. The label links into the Spend drill-down.
import { RouterLink } from 'vue-router'
import { topRowLocation, type TopLevel } from '../costs'
import type { CostsTopView, CostTopRowView } from '../generated/views'
import { spendUSD, unknownNote } from '../spend'
import DataList, { type DataListColumn } from '../ui/DataList.vue'

defineProps<{ top: CostsTopView }>()

const lists: { level: TopLevel; title: string; key: keyof CostsTopView }[] = [
  { level: 'milestone', title: 'Milestones', key: 'milestones' },
  { level: 'issue', title: 'Issues', key: 'issues' },
  { level: 'run', title: 'Runs', key: 'runs' },
]

function columns(level: TopLevel): DataListColumn[] {
  return [
    { key: 'label', label: 'Name' },
    { key: 'project', label: 'Project' },
    ...(level === 'run' ? [{ key: 'kind', label: 'Kind' }] : []),
    { key: 'cost', label: 'Known cost', numeric: true },
  ]
}

const rowKey = (r: CostTopRowView) => `${r.namespace}/${r.project}/${r.milestoneIID ?? 0}/${r.issueIID ?? 0}/${r.task ?? ''}/${r.run ?? 0}`
const link = 'text-(--accent) underline-offset-2 hover:underline [overflow-wrap:anywhere]'
</script>

<template>
  <section class="section" aria-labelledby="costs-top" data-costs-top>
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="costs-top">Most expensive</h2>
      <span class="faint">By known cost in the range; a row opens its Spend drill-down</span>
    </div>
    <div class="grid min-w-0 gap-3 desktop:grid-cols-3">
      <div v-for="l in lists" :key="l.level" class="card min-w-0 overflow-hidden" :data-top="l.level">
        <h3 class="m-0 px-3.5 pt-3 text-[13px] font-semibold">{{ l.title }}</h3>
        <p v-if="!top[l.key].length" class="faint m-0 px-3.5 py-3">None in this range.</p>
        <DataList v-else :columns="columns(l.level)" :rows="top[l.key]" :row-key="rowKey">
          <template #cell-label="{ row }">
            <RouterLink :to="topRowLocation(l.level, row)" :class="link">{{ row.label }}</RouterLink>
          </template>
          <template #cell-project="{ row }"><span class="[overflow-wrap:anywhere]">{{ row.namespace }}/{{ row.project }}</span></template>
          <template #cell-kind="{ row }">{{ row.kind ?? '' }}</template>
          <template #cell-cost="{ row }">
            <span :class="{ italic: !row.totals.costKnown }">{{ spendUSD(row.totals) }}</span>
            <span v-if="unknownNote(row.totals)" class="faint ml-1" data-top-unknown>{{ unknownNote(row.totals) }}</span>
          </template>
        </DataList>
      </div>
    </div>
  </section>
</template>
