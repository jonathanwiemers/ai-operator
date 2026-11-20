<script setup lang="ts">
import UEmpty from '@nuxt/ui/components/Empty.vue'
// Flow (docs/contributing/web-ui.md, "Metrics page"; R14 of the milestone
// "Desk: one Metrics page"): the issues merged per bucket, their median wall
// and agent times and the review and fix calls per merged issue. Kind and
// engine do not apply.
import { computed } from 'vue'
import type { MetricsFlowView } from '../generated/views'
import { flowChart, flowTiles } from '../metricsFlow'
import { metricsFilterNote, pausesChartSummary } from '../metricsPauses'
import ChartLegend from '../ui/charts/ChartLegend.vue'
import ChartTable from '../ui/charts/ChartTable.vue'
import StackedBars from '../ui/charts/StackedBars.vue'
import StatTile from './StatTile.vue'

const props = defineProps<{ flow: MetricsFlowView; filtered: boolean }>()

const chart = computed(() => flowChart(props.flow))
const title = computed(() => `Merged issues per ${props.flow.bucket}`)
const tiles = computed(() => flowTiles(props.flow.totals))
</script>

<template>
  <section class="section" aria-labelledby="metrics-flow" data-metrics-flow>
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="metrics-flow">Flow</h2>
      <span class="faint">Issues merged in the range, each with all its runs and calls</span>
    </div>
    <p v-if="filtered" class="faint mt-0 mb-2" data-metrics-filter-note>{{ metricsFilterNote }}</p>

    <div class="stat-tiles" data-flow-tiles>
      <StatTile v-for="t in tiles" :key="t.id" :label="t.label" :value="t.value" :data-tile="t.id" />
    </div>

    <UEmpty v-if="!flow.totals.mergedIssues" icon="i-lucide-git-pull-request" title="No issue merged in this range." size="sm" class="mt-3" data-flow-empty />
    <div v-else class="card mt-3 flex min-w-0 flex-col gap-3 p-3 tablet:p-4" data-chart="flow">
      <h3 class="m-0 text-[13px] font-semibold">{{ title }}</h3>
      <StackedBars :chart="chart" :summary="pausesChartSummary(title, chart, 'count')" unit="count" />
      <ChartLegend :chart="chart" unit="count" />
      <ChartTable :chart="chart" unit="count" :label="`${title}, as a table`" />
    </div>
  </section>
</template>
