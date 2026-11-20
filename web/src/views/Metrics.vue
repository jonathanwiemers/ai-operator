<script setup lang="ts">
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UFormField from '@nuxt/ui/components/FormField.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import USelect from '@nuxt/ui/components/Select.vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, getCosts, getCostsMetrics, getCostsTop, getMetricsCode, getMetricsFlow, getMetricsPauses, getProjects, LoginRequiredError, NotFoundError } from '../client'
import {
  changeLabel,
  costChange,
  costEngines,
  costFiltersQuery,
  costGroups,
  costKinds,
  costParams,
  breakdownsCsv,
  downloadCsv,
  seriesCsv,
  costRanges,
  monthToDate,
  parseCostFilters,
  rangeLabels,
  type CostFilters,
} from '../costs'
import { chartSummary, cumulativeMonth, monthSummary, stackedChart, thisMonth, tokensChart, tokensSummary } from '../chartData'
import { cost, tokenCount } from '../format'
import { pageHeaderUi } from '../pageHeader'
import { basisNote, basisTitle, shareLabel, spendTokens, spendUSD, tokenBreakdown, unmeteredNote } from '../spend'
import type { CostsTopView, CostsView, MetricsCodeView, MetricsFlowView, MetricsPausesView, MetricsView } from '../generated/views'
import BudgetsSection from '../components/BudgetsSection.vue'
import MetricsBreakdown from '../components/MetricsBreakdown.vue'
import MetricsCode from '../components/MetricsCode.vue'
import MetricsContext from '../components/MetricsContext.vue'
import MetricsFlow from '../components/MetricsFlow.vue'
import MetricsPauses from '../components/MetricsPauses.vue'
import MostExpensive from '../components/MostExpensive.vue'
import PlanUsage from '../components/PlanUsage.vue'
import StatTile from '../components/StatTile.vue'
import WhereItGoes from '../components/WhereItGoes.vue'
import ChartLegend from '../ui/charts/ChartLegend.vue'
import ChartTable from '../ui/charts/ChartTable.vue'
import LineCompare from '../ui/charts/LineCompare.vue'
import StackedBars from '../ui/charts/StackedBars.vue'

// The Costs page (docs/contributing/web-ui.md, "Metrics page"): what the projects the viewer
// may see, and research, cost over a range. The filters live in the URL
// (src/costs.ts); a change replaces the query and refetches, no reload.
// The KPI row, then the charts (src/ui/charts, Chart.js in this chunk only).
const route = useRoute()
const router = useRouter()

// The browser's zone, never in the URL (decision D362-P3): a shared link
// shows the same range in the viewer's own days.
const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const filters = computed(() => parseCostFilters(route.query))

const data = ref<CostsView | null>(null)
const top = ref<CostsTopView | null>(null)
const metrics = ref<MetricsView | null>(null)
const code = ref<MetricsCodeView | null>(null)
const flow = ref<MetricsFlowView | null>(null)
const pauses = ref<MetricsPausesView | null>(null)
const loading = ref(false)
const error = ref('')
let seq = 0

async function load() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const params = costParams(filters.value, tz)
    // The top lists, the metrics, code, flow and pauses are side parts: their
    // failure leaves the page standing (D606-P1).
    const [v, t, m, c, fl, p] = await Promise.all([
      getCosts(params),
      getCostsTop(params).catch(() => null),
      getCostsMetrics(params).catch(() => null),
      getMetricsCode(params).catch(() => null),
      getMetricsFlow(params).catch(() => null),
      getMetricsPauses(params).catch(() => null),
    ])
    if (mine !== seq) return
    data.value = v
    top.value = t
    metrics.value = m
    code.value = c
    flow.value = fl
    pauses.value = p
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return
    data.value = null
    top.value = null
    metrics.value = null
    code.value = null
    flow.value = null
    pauses.value = null
    if (e instanceof NotFoundError) {
      error.value = filters.value.project
        ? `There is no project ${filters.value.project} you may see.`
        : 'Costs need the history database, which is not configured.'
    } else if (e instanceof ApiError && e.status === 503) {
      error.value = 'The history database is not connected yet.'
    } else {
      error.value = e instanceof Error ? e.message : String(e)
    }
  } finally {
    if (mine === seq) loading.value = false
  }
}

watch(() => JSON.stringify(filters.value), () => void load(), { immediate: true })

function setFilters(patch: Partial<CostFilters>) {
  void router.replace({ query: costFiltersQuery({ ...filters.value, ...patch }) })
}

// The projects for the project filter: the viewer's, loaded once.
const projects = ref<string[]>([])
getProjects()
  .then((ps) => (projects.value = ps.map((p) => `${p.namespace}/${p.name}`).sort()))
  .catch(() => undefined)

// Selects take no empty value: "all" stands for no filter.
const all = 'all'
const rangeItems = [...costRanges, 'custom' as const].map((r) => ({ value: r, label: rangeLabels[r] }))
const groupItems = costGroups.map((g) => ({ value: g, label: g }))
const kindItems = [{ value: all, label: 'All kinds' }, ...costKinds.map((k) => ({ value: k, label: k }))]
const engineItems = [{ value: all, label: 'All engines' }, ...costEngines.map((e) => ({ value: e, label: e }))]
const projectItems = computed(() => {
  const refs = new Set(projects.value)
  if (filters.value.project) refs.add(filters.value.project)
  return [{ value: all, label: 'All projects' }, ...[...refs].map((p) => ({ value: p, label: p }))]
})

function onRange(v: string) {
  if (v === 'custom') {
    // Custom starts from the range shown.
    const from = data.value?.from ?? filters.value.from
    const to = data.value?.to ?? filters.value.to
    if (from && to) setFilters({ range: 'custom', from, to })
    return
  }
  setFilters({ range: v as CostFilters['range'], from: undefined, to: undefined })
}

function onDate(which: 'from' | 'to', v: string | number) {
  const next = { ...filters.value, [which]: String(v) }
  if (next.from && next.to && next.from <= next.to) setFilters({ range: 'custom', from: next.from, to: next.to })
}

function onFilter(key: 'project' | 'kind' | 'engine', v: string) {
  setFilters({ [key]: v === all ? undefined : v })
}

const totals = computed(() => data.value?.totals)
const change = computed(() => (data.value ? changeLabel(costChange(data.value.totals, data.value.previous)) : ''))
const month = computed(() => (data.value ? monthToDate(data.value.month) : null))
const monthChange = computed(() => changeLabel(month.value?.change ?? null))

function dateLabel(iso: string | undefined): string {
  if (!iso) return ''
  const t = new Date(iso)
  return Number.isNaN(t.getTime()) ? '' : t.toLocaleDateString()
}

// Spend over time (R9): known cost per bucket, stacked by the chosen group,
// top six plus Other.
const spendChart = computed(() => (data.value ? stackedChart(data.value.buckets, data.value.bucket, data.value.series, 'cost') : null))
const spendTitle = computed(() => (data.value ? `Known cost per ${data.value.bucket} by ${data.value.group}` : ''))
const spendSummary = computed(() => (spendChart.value ? chartSummary(spendTitle.value, spendChart.value) : ''))

// This month (R10): cumulative known cost to today, last month dashed over
// its own days, by day of month; independent of the range.
const monthChart = computed(() => (data.value ? cumulativeMonth(data.value.month, thisMonth(data.value.month, new Date(), tz)) : null))

// Tokens (R12): metered tokens per bucket by type, the cache-read share as a
// line. Calls not metered are named, not drawn as 0-token bars.
const tokens = computed(() => (data.value ? tokensChart(data.value.buckets, data.value.bucket, data.value.series) : null))

// Download CSV (R16): built here from the response, no request.
// It is the header's secondary action (decision 4), a menu of the two files.
const csvMenu = ref<HTMLDetailsElement | null>(null)
function download(what: 'series' | 'breakdowns') {
  const d = data.value
  if (!d) return
  if (what === 'series') downloadCsv(`costs-series-${d.from}-${d.to}.csv`, seriesCsv(d))
  else downloadCsv(`costs-breakdowns-${d.from}-${d.to}.csv`, breakdownsCsv(d))
  if (csvMenu.value) csvMenu.value.open = false
}
// The meta line: the range shown, and Loading… while a new one loads.
const rangeLine = computed(() => {
  const d = data.value
  if (!d) return undefined
  return `${d.from} to ${d.to}, ${d.tz}${loading.value ? ' · Loading…' : ''}`
})

const fieldUI = { label: 'font-semibold text-[13px]' }
const control = 'text-base/5 tablet:text-sm/5'
const selectProps = {
  trailingIcon: 'i-lucide-chevron-down',
  selectedIcon: 'i-lucide-check',
  class: 'min-h-11 w-full tablet:min-h-0 tablet:w-auto tablet:min-w-36',
  ui: { base: control, item: 'min-h-11 tablet:min-h-0' },
} as const
</script>

<template>
  <div class="page">
    <UPageHeader title="Metrics" :description="rangeLine" :ui="pageHeaderUi">
      <template v-if="data && data.totals.calls > 0" #links>
        <!-- A <details> menu, not UDropdownMenu: that adds 16 kB gzipped to
             this route, past its bundle budget. -->
        <details ref="csvMenu" class="relative" data-costs-csv>
          <summary class="card flex min-h-11 cursor-pointer list-none items-center px-3 text-sm font-semibold tablet:min-h-0 tablet:py-1.5">Download CSV</summary>
          <div class="card absolute end-0 z-10 mt-1 flex w-52 flex-col gap-1 p-2">
            <UButton color="neutral" variant="ghost" label="Series" data-csv="series" @click="download('series')" />
            <UButton color="neutral" variant="ghost" label="Breakdowns" data-csv="breakdowns" @click="download('breakdowns')" />
          </div>
        </details>
      </template>
    </UPageHeader>

    <form class="mb-4 flex flex-col gap-3 tablet:flex-row tablet:flex-wrap tablet:items-end" aria-label="Filters" data-costs-filters @submit.prevent>
      <UFormField label="Range" name="range" :ui="fieldUI">
        <USelect :model-value="filters.range" :items="rangeItems" v-bind="selectProps" data-filter="range" @update:model-value="onRange" />
      </UFormField>
      <template v-if="filters.range === 'custom'">
        <UFormField label="From" name="from" :ui="fieldUI">
          <UInput
            type="date"
            :model-value="filters.from"
            :min="data?.earliest"
            class="w-full tablet:w-auto"
            :ui="{ base: control }"
            @update:model-value="onDate('from', $event)"
          />
        </UFormField>
        <UFormField label="To" name="to" :ui="fieldUI">
          <UInput type="date" :model-value="filters.to" class="w-full tablet:w-auto" :ui="{ base: control }" @update:model-value="onDate('to', $event)" />
        </UFormField>
      </template>
      <UFormField label="Group by" name="group" :ui="fieldUI">
        <USelect
          :model-value="filters.group"
          :items="groupItems"
          data-filter="group"
          v-bind="selectProps"
          @update:model-value="setFilters({ group: $event as CostFilters['group'] })"
        />
      </UFormField>
      <UFormField label="Project" name="project" :ui="fieldUI">
        <USelect :model-value="filters.project ?? all" :items="projectItems" v-bind="selectProps" data-filter="project" @update:model-value="onFilter('project', $event)" />
      </UFormField>
      <UFormField label="Kind" name="kind" :ui="fieldUI">
        <USelect :model-value="filters.kind ?? all" :items="kindItems" v-bind="selectProps" data-filter="kind" @update:model-value="onFilter('kind', $event)" />
      </UFormField>
      <UFormField label="Engine" name="engine" :ui="fieldUI">
        <USelect :model-value="filters.engine ?? all" :items="engineItems" v-bind="selectProps" data-filter="engine" @update:model-value="onFilter('engine', $event)" />
      </UFormField>
    </form>

    <div v-if="error" class="banner danger [overflow-wrap:anywhere]" data-costs-error>{{ error }}</div>
    <div v-else-if="!data || !totals" class="card empty">Loading…</div>
    <UEmpty
      v-else-if="totals.calls === 0"
      icon="i-lucide-receipt"
      title="No calls"
      description="No agent call of the projects you may see falls into this range and these filters."
      data-costs-empty
    />
    <template v-else>
      <!-- As many tiles as fit, six in one row on desktop. -->
      <div class="stat-tiles desktop:grid-cols-6" data-costs-kpis>
        <StatTile label="Known cost" :sub="change ? `${change} vs the period before` : 'no previous cost to compare'" data-kpi="cost">
          <span :class="{ italic: !totals.costKnown }">{{ spendUSD(totals) }}</span>
        </StatTile>
        <StatTile
          v-if="month"
          label="Month to date"
          :sub="`${month.previous.costKnown ? cost(month.previous.costUSD) : 'n/a'} the same days last month${monthChange ? ` (${monthChange})` : ''}`"
          data-kpi="month"
        >
          <span :class="{ italic: !month.current.costKnown }">{{ month.current.costKnown ? cost(month.current.costUSD) : 'n/a' }}</span>
        </StatTile>
        <StatTile label="Calls" :value="totals.calls" :sub="totals.subagentCalls ? `${totals.subagentCalls} subagent` : undefined" data-kpi="calls" />
        <StatTile
          label="Metered tokens"
          :value="tokenCount(spendTokens(totals))"
          :sub="`${tokenCount(totals.inputTokens)} in · ${tokenCount(totals.outputTokens)} out`"
          :title="tokenBreakdown(totals)"
          data-kpi="tokens"
        />
        <StatTile
          label="Cache-read share"
          :value="shareLabel(data.cacheReadShare)"
          :sub="data.previous.calls ? `${shareLabel(data.previousCacheReadShare)} the period before` : undefined"
          title="Cache reads of all input tokens (input, cache read and cache write)"
          data-kpi="cache"
        />
        <StatTile
          label="Unknown cost"
          :value="`${totals.unknownCalls} ${totals.unknownCalls === 1 ? 'call' : 'calls'}`"
          title="Calls without a reported or estimated cost are not in the sum"
          data-kpi="unknown"
        >
          <template v-if="unmeteredNote(totals)" #sub>
            <span title="Calls that report neither a cost nor tokens (cursor's model auto)">{{ unmeteredNote(totals) }}</span>
          </template>
        </StatTile>
      </div>
      <p v-if="data.researchSince" class="faint mt-2" data-costs-research>
        Research counted since {{ dateLabel(data.researchSince) }}
      </p>

      <section v-if="spendChart" class="section" aria-labelledby="costs-spend-over-time" data-chart="spend">
        <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 id="costs-spend-over-time">Spend over time</h2>
          <span class="faint">{{ spendTitle }}</span>
        </div>
        <div class="card flex min-w-0 flex-col gap-3 p-3 tablet:p-4">
          <StackedBars :chart="spendChart" :summary="spendSummary" />
          <ChartLegend :chart="spendChart" />
          <ChartTable :chart="spendChart" :label="`${spendTitle}, as a table`" />
        </div>
      </section>

      <section v-if="monthChart" class="section" aria-labelledby="costs-this-month" data-chart="month">
        <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 id="costs-this-month">This month</h2>
          <span class="faint">Known cost to date, against last month by day of month</span>
        </div>
        <div class="card flex min-w-0 flex-col gap-3 p-3 tablet:p-4">
          <LineCompare :chart="monthChart" :summary="monthSummary(monthChart)" title="Day " />
          <ChartLegend :chart="monthChart" />
          <ChartTable
            :chart="monthChart"
            :sum="false"
            row-label="Day"
            label="Cumulative known cost by day of month, this month and last month, as a table"
          />
        </div>
      </section>
      <WhereItGoes :data="data" :filters="filters" />

      <section v-if="tokens" class="section" aria-labelledby="costs-tokens" data-chart="tokens">
        <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 id="costs-tokens">Tokens</h2>
          <span class="faint">Metered tokens per {{ data.bucket }}, the line is the cache-read share</span>
        </div>
        <div class="card flex min-w-0 flex-col gap-3 p-3 tablet:p-4">
          <p v-if="basisNote(totals)" class="faint m-0" :title="basisTitle" data-costs-basis>{{ basisNote(totals) }}</p>
          <p v-if="unmeteredNote(totals)" class="faint m-0" data-costs-unmetered>
            {{ unmeteredNote(totals) }}: no tokens to draw, so no bars for them.
          </p>
          <StackedBars :chart="tokens.bars" :line="tokens.line" :summary="tokensSummary(tokens)" unit="tokens" />
          <ChartLegend :chart="tokens.view" unit="tokens" />
          <ChartTable :chart="tokens.view" unit="tokens" label="Metered tokens per bucket by type and the cache-read share, as a table" />
        </div>
      </section>

      <MostExpensive v-if="top" :top="top" />
      <template v-if="metrics">
        <MetricsBreakdown :metrics="metrics" :filtered="!!(filters.kind || filters.engine)" />
        <MetricsContext :context="metrics.context" />
      </template>
      <PlanUsage :usage="data.planUsage" />
      <BudgetsSection :budgets="data.budgets" />
      <MetricsCode v-if="code" :code="code" :filtered="!!(filters.kind || filters.engine)" :project="filters.project" />
      <MetricsFlow v-if="flow" :flow="flow" :filtered="!!(filters.kind || filters.engine)" />
      <MetricsPauses v-if="pauses" :pauses="pauses" :filtered="!!(filters.kind || filters.engine)" :project="filters.project" />
    </template>
  </div>
</template>
