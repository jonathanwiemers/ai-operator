<script setup lang="ts">
// Where it goes (docs/ui.md, "Costs page"): known cost by project, kind,
// model or engine as horizontal bars, top six plus Other, with a legend of
// values and a table view. Each row of a project, kind or engine is a link
// that filters the whole page (costs.ts's breakdownLink); a click on its bar
// does the same. Model rows and Other are not links.
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { breakdownRows, breakdownSummary, formatUSD, unknownNote, type BreakdownRow, type ChartToken } from '../chartData'
import { costGroups, type CostFilters, type CostGroup } from '../costs'
import type { CostsView } from '../generated/views'
import DataList, { type DataListColumn } from '../ui/DataList.vue'
import HorizontalBars from '../ui/charts/HorizontalBars.vue'

const props = defineProps<{ data: CostsView; filters: CostFilters }>()
const router = useRouter()

const dimension = ref<CostGroup>('project')
const tableOpen = ref(false)

const source = computed(() => {
  const d = props.data
  return { project: d.byProject, kind: d.byKind, model: d.byModel, engine: d.byEngine }[dimension.value]
})
const rows = computed(() => breakdownRows(source.value, dimension.value, props.filters))
const summary = computed(() => breakdownSummary(dimension.value, rows.value))
const total = computed(() => rows.value.reduce((a, r) => a + r.value, 0))

function open(i: number) {
  const link = rows.value[i]?.link
  if (link) void router.push(link)
}

// Whole class names, so Tailwind finds them; no inline style (the CSP).
const swatch: Record<ChartToken, string> = {
  '--chart-1': 'bg-(--chart-1)',
  '--chart-2': 'bg-(--chart-2)',
  '--chart-3': 'bg-(--chart-3)',
  '--chart-4': 'bg-(--chart-4)',
  '--chart-5': 'bg-(--chart-5)',
  '--chart-6': 'bg-(--chart-6)',
  '--chart-other': 'bg-(--chart-other)',
}

const columns: DataListColumn[] = [
  { key: 'label', label: 'Name' },
  { key: 'cost', label: 'Known cost', numeric: true },
  { key: 'unknown', label: 'Unknown cost' },
]
const tableRows = computed(() =>
  rows.value.map((r, i) => ({ id: String(i), row: r, label: r.label, cost: r.total === null ? 'n/a' : formatUSD(r.total), unknown: unknownNote(r.unknown) })),
)
type TableRow = (typeof tableRows.value)[number]

const link = 'text-(--accent) underline-offset-2 hover:underline'
const keyOf = (r: BreakdownRow) => (r.key === '\u0000other' ? 'other' : r.key)
</script>

<template>
  <section class="section" aria-labelledby="costs-where-it-goes" data-chart="where">
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="costs-where-it-goes">Where it goes</h2>
      <span class="faint">Known cost by {{ dimension }}; a row filters the page</span>
    </div>
    <div class="card flex min-w-0 flex-col gap-3 p-3 tablet:p-4">
      <div class="flex flex-wrap gap-2" role="group" aria-label="Break down by">
        <UButton
          v-for="g in costGroups"
          :key="g"
          :label="g"
          color="neutral"
          size="sm"
          class="min-h-11 tablet:min-h-0"
          :variant="dimension === g ? 'soft' : 'outline'"
          :aria-pressed="dimension === g"
          :data-where-dimension="g"
          @click="dimension = g"
        />
      </div>
      <HorizontalBars :rows="rows" :summary="summary" @select="open" />
      <ul class="m-0 flex list-none flex-col gap-1 p-0 text-[13px]" aria-label="Legend" data-chart-legend>
        <li v-for="r in rows" :key="r.key" class="flex min-w-0 items-baseline gap-1.5" :data-legend-key="keyOf(r)">
          <i class="inline-block size-2.5 shrink-0 self-center rounded-xs" :class="swatch[r.token]" aria-hidden="true" />
          <RouterLink v-if="r.link" :to="r.link" :class="[link, 'min-w-0 [overflow-wrap:anywhere]']">{{ r.label }}</RouterLink>
          <span v-else class="min-w-0 [overflow-wrap:anywhere]">{{ r.label }}</span>
          <span class="font-semibold tabular-nums" :class="{ italic: r.total === null }">{{ r.total === null ? 'n/a' : formatUSD(r.total) }}</span>
          <span v-if="r.unknown" class="text-(--text-2)">{{ unknownNote(r.unknown) }}</span>
        </li>
      </ul>
      <div data-chart-table>
        <UButton
          label="Table"
          icon="i-lucide-table"
          color="neutral"
          :variant="tableOpen ? 'soft' : 'outline'"
          size="sm"
          class="min-h-11 tablet:min-h-0"
          :aria-pressed="tableOpen"
          data-chart-table-toggle
          @click="tableOpen = !tableOpen"
        />
        <div :class="tableOpen ? 'mt-3 max-h-96 overflow-y-auto' : 'sr-only'" role="group" :aria-label="`Known cost by ${dimension}, as a table`">
          <DataList :columns="columns" :rows="tableRows" :row-key="(r: TableRow) => r.id">
            <template #cell-label="{ row }">
              <RouterLink v-if="row.row.link" :to="row.row.link" :class="link">{{ row.label }}</RouterLink>
              <template v-else>{{ row.label }}</template>
            </template>
          </DataList>
        </div>
        <p class="faint mt-2">Total known: {{ formatUSD(total) }}</p>
      </div>
    </div>
  </section>
</template>
