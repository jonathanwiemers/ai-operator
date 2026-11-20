<script setup lang="ts">
// Code (docs/contributing/web-ui.md, "Metrics page"; R14 of the milestone
// "Desk: one Metrics page"): the written and merged production lines per
// bucket, the range's tiles and, without a project filter, the merged
// production lines and cost per 100 of each project. Kind and engine do not
// apply.
import { computed } from 'vue'
import type { MetricsCodeView } from '../generated/views'
import { codeChart, codeProjectRows, codeTiles, type CodeProjectRow } from '../metricsCode'
import { metricsFilterNote, pausesChartSummary } from '../metricsPauses'
import ChartLegend from '../ui/charts/ChartLegend.vue'
import ChartTable from '../ui/charts/ChartTable.vue'
import StackedBars from '../ui/charts/StackedBars.vue'
import StatTile from './StatTile.vue'
import DataList, { type DataListColumn } from '../ui/DataList.vue'

const props = defineProps<{ code: MetricsCodeView; filtered: boolean; project?: string }>()

const chart = computed(() => codeChart(props.code))
const title = computed(() => `Written and merged production lines per ${props.code.bucket}`)
const tiles = computed(() => codeTiles(props.code.totals))
const projects = computed(() => (props.project ? [] : codeProjectRows(props.code)))

const projectColumns: DataListColumn[] = [
  { key: 'project', label: 'Project', class: '[overflow-wrap:anywhere]' },
  { key: 'production', label: 'Merged production lines', numeric: true },
  { key: 'per100', label: 'Cost per 100 lines', numeric: true },
]
const projectKey = (r: CodeProjectRow) => r.project
</script>

<template>
  <section class="section" aria-labelledby="metrics-code" data-metrics-code>
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="metrics-code">Code</h2>
      <span class="faint">Lines the agents wrote, and the lines merge requests merged, by category</span>
    </div>
    <p v-if="filtered" class="faint mt-0 mb-2" data-metrics-filter-note>{{ metricsFilterNote }}</p>

    <div class="stat-tiles" data-code-tiles>
      <StatTile v-for="t in tiles" :key="t.id" :label="t.label" :value="t.value" :sub="t.note || undefined" :data-tile="t.id" />
    </div>

    <div class="mt-3 grid min-w-0 gap-3">
      <div class="card flex min-w-0 flex-col gap-3 p-3 tablet:p-4" data-chart="code">
        <h3 class="m-0 text-[13px] font-semibold">{{ title }}</h3>
        <StackedBars :chart="chart" :summary="pausesChartSummary(title, chart, 'count')" unit="count" grouped />
        <ChartLegend :chart="chart" unit="count" />
        <ChartTable :chart="chart" unit="count" :sum="false" :label="`${title}, as a table`" />
      </div>
      <div v-if="projects.length" class="card min-w-0 overflow-hidden" data-code-table="project">
        <h3 class="m-0 px-3.5 pt-3 pb-2 text-[13px] font-semibold">Per project</h3>
        <DataList :columns="projectColumns" :rows="projects" :row-key="projectKey" />
      </div>
    </div>
  </section>
</template>
