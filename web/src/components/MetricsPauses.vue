<script setup lang="ts">
import UEmpty from '@nuxt/ui/components/Empty.vue'
// Pauses (docs/contributing/web-ui.md, "Metrics page"; decision 10 of the
// milestone "Desk: one Metrics page"): the milestone pauses that started in the
// range, per bucket by kind and in paused hours, the totals, who and how
// ended them, per project without a project filter, and the ten longest,
// each opening its milestone's Pilot tab. Kind and engine do not apply.
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { MetricsPausesView } from '../generated/views'
import {
  longPauseRows,
  metricsFilterNote,
  pauseKindsChart,
  pausedHoursChart,
  pauseProjectRows,
  pausesChartSummary,
  pausesTruncatedNote,
  pauseTables,
  pauseTiles,
  type LongPauseRow,
  type PauseKindTableRow,
  type PauseProjectRow,
} from '../metricsPauses'
import type { PauseResolverRow, PauseViaRow } from '../pauses'
import ChartLegend from '../ui/charts/ChartLegend.vue'
import ChartTable from '../ui/charts/ChartTable.vue'
import StackedBars from '../ui/charts/StackedBars.vue'
import StatTile from './StatTile.vue'
import DataList, { type DataListColumn } from '../ui/DataList.vue'

const props = defineProps<{ pauses: MetricsPausesView; filtered: boolean; project?: string }>()

const kinds = computed(() => pauseKindsChart(props.pauses))
const kindsTitle = computed(() => `Pauses per ${props.pauses.bucket} by kind`)
const hours = computed(() => pausedHoursChart(props.pauses))
const hoursTitle = computed(() => `Paused hours per ${props.pauses.bucket}`)
const tiles = computed(() => pauseTiles(props.pauses))
const tables = computed(() => pauseTables(props.pauses))
const projects = computed(() => (props.project ? [] : pauseProjectRows(props.pauses)))
const longest = computed(() => longPauseRows(props.pauses))

const kindColumns: DataListColumn[] = [
  { key: 'label', label: 'Kind' },
  { key: 'count', label: 'Pauses', numeric: true },
  { key: 'duration', label: 'Time', numeric: true },
]
const resolverColumns: DataListColumn[] = [
  { key: 'label', label: 'Resolved by' },
  { key: 'count', label: 'Pauses', numeric: true },
]
const viaColumns: DataListColumn[] = [
  { key: 'label', label: 'Via' },
  { key: 'count', label: 'Pauses', numeric: true },
]
const projectColumns: DataListColumn[] = [
  { key: 'project', label: 'Project', class: '[overflow-wrap:anywhere]' },
  { key: 'count', label: 'Pauses', numeric: true },
  { key: 'duration', label: 'Time', numeric: true },
]
const longColumns: DataListColumn[] = [
  { key: 'project', label: 'Project', class: '[overflow-wrap:anywhere]' },
  { key: 'milestone', label: 'Milestone' },
  { key: 'kind', label: 'Kind' },
  { key: 'reason', label: 'Reason', class: '[overflow-wrap:anywhere]' },
  { key: 'duration', label: 'Length', numeric: true },
]

const kindKey = (r: PauseKindTableRow) => r.kind
const resolverKey = (r: PauseResolverRow) => r.resolver
const viaKey = (r: PauseViaRow) => r.via
const projectKey = (r: PauseProjectRow) => r.project
const longKey = (r: LongPauseRow) => r.id

const link = 'row-link underline-offset-2 hover:underline'
const tableCard = 'card min-w-0 overflow-hidden'
const tableTitle = 'm-0 px-3.5 pt-3 pb-2 text-[13px] font-semibold'
</script>

<template>
  <section class="section" aria-labelledby="metrics-pauses" data-metrics-pauses>
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="metrics-pauses">Pauses</h2>
      <span class="faint">Milestone pauses that started in the range, open ones up to now</span>
    </div>
    <p v-if="filtered" class="faint mt-0 mb-2" data-metrics-filter-note>{{ metricsFilterNote }}</p>
    <p v-if="pauses.truncated" class="banner mt-0 mb-2" data-pauses-truncated>{{ pausesTruncatedNote }}</p>

    <div class="stat-tiles" data-pauses-tiles>
      <StatTile v-for="t in tiles" :key="t.id" :label="t.label" :value="t.value" :data-tile="t.id" />
    </div>

    <UEmpty v-if="!pauses.totals.count" icon="i-lucide-flag" title="No milestone paused in this range." size="sm" class="mt-3" data-pauses-empty />
    <div v-else class="mt-3 grid min-w-0 gap-3">
      <div class="card flex min-w-0 flex-col gap-3 p-3 tablet:p-4" data-chart="pauses">
        <h3 class="m-0 text-[13px] font-semibold">{{ kindsTitle }}</h3>
        <StackedBars :chart="kinds" :summary="pausesChartSummary(kindsTitle, kinds, 'count')" unit="count" />
        <ChartLegend :chart="kinds" unit="count" />
        <ChartTable :chart="kinds" unit="count" :label="`${kindsTitle}, as a table`" />
      </div>
      <div class="card flex min-w-0 flex-col gap-3 p-3 tablet:p-4" data-chart="paused-hours">
        <h3 class="m-0 text-[13px] font-semibold">{{ hoursTitle }}</h3>
        <StackedBars :chart="hours" :summary="pausesChartSummary(hoursTitle, hours, 'hours')" unit="hours" />
        <ChartTable :chart="hours" unit="hours" :label="`${hoursTitle}, as a table`" />
      </div>

      <div class="grid min-w-0 gap-3 tablet:grid-cols-3">
        <div :class="tableCard" data-pauses-table="kind">
          <h3 :class="tableTitle">By kind</h3>
          <DataList :columns="kindColumns" :rows="tables.kinds" :row-key="kindKey" />
        </div>
        <div :class="tableCard" data-pauses-table="resolver">
          <h3 :class="tableTitle">By resolver</h3>
          <p v-if="!tables.resolvers.length" class="faint m-0 px-3.5 pb-3">None resumed yet.</p>
          <DataList v-else :columns="resolverColumns" :rows="tables.resolvers" :row-key="resolverKey" />
        </div>
        <div :class="tableCard" data-pauses-table="via">
          <h3 :class="tableTitle">By via</h3>
          <p v-if="!tables.vias.length" class="faint m-0 px-3.5 pb-3">None resumed yet.</p>
          <DataList v-else :columns="viaColumns" :rows="tables.vias" :row-key="viaKey" />
        </div>
      </div>

      <div v-if="projects.length" :class="tableCard" data-pauses-table="project">
        <h3 :class="tableTitle">Per project</h3>
        <DataList :columns="projectColumns" :rows="projects" :row-key="projectKey" />
      </div>

      <div v-if="longest.length" :class="tableCard" data-pauses-table="longest">
        <h3 :class="tableTitle">Longest pauses</h3>
        <DataList :columns="longColumns" :rows="longest" :row-key="longKey">
          <template #cell-milestone="{ row }">
            <RouterLink :to="row.to" :class="link" data-long-pause>{{ row.milestone }}</RouterLink>
          </template>
          <template #cell-duration="{ row }">
            {{ row.duration }}<span v-if="row.open" class="faint"> · open</span>
          </template>
        </DataList>
      </div>
    </div>
  </section>
</template>
