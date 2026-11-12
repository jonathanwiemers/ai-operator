<script setup lang="ts">
import UFormField from '@nuxt/ui/components/FormField.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import USelect from '@nuxt/ui/components/Select.vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError, getCosts, getProjects, LoginRequiredError, NotFoundError } from '../client'
import {
  changeLabel,
  costChange,
  costEngines,
  costFiltersQuery,
  costGroups,
  costKinds,
  costParams,
  costRanges,
  monthToDate,
  parseCostFilters,
  rangeLabels,
  type CostFilters,
} from '../costs'
import { cost, tokenCount } from '../format'
import { basisNote, basisTitle, shareLabel, spendTokens, spendUSD, tokenBreakdown, unmeteredNote } from '../spend'
import type { CostsView } from '../generated/views'

// The Costs page (docs/ui.md, "Costs page"): what the projects the viewer
// may see, and research, cost over a range. The filters live in the URL
// (src/costs.ts); a change replaces the query and refetches, no reload.
// This slice has the KPI row; the charts follow (R8 to R16).
const route = useRoute()
const router = useRouter()

// The browser's zone, never in the URL (decision D362-P3): a shared link
// shows the same range in the viewer's own days.
const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const filters = computed(() => parseCostFilters(route.query))

const data = ref<CostsView | null>(null)
const loading = ref(false)
const error = ref('')
let seq = 0

async function load() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const v = await getCosts(costParams(filters.value, tz))
    if (mine !== seq) return
    data.value = v
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return
    data.value = null
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

const fieldUI = { label: 'font-semibold text-[13px]' }
const control = 'text-base/5 tablet:text-sm/5'
const selectProps = {
  trailingIcon: 'i-lucide-chevron-down',
  selectedIcon: 'i-lucide-check',
  class: 'min-h-11 w-full tablet:min-h-0 tablet:w-auto tablet:min-w-36',
  ui: { base: control, item: 'min-h-11 tablet:min-h-0' },
} as const
const kpi = 'card flex min-w-0 flex-col gap-1 px-4 py-3'
const kpiLabel = 'text-[11.5px] text-(--text-3)'
const kpiValue = 'text-xl font-semibold tabular-nums'
const kpiSub = 'text-[13px] text-(--text-2) [overflow-wrap:anywhere]'
</script>

<template>
  <div class="page">
    <div class="mb-3 flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h1>Costs</h1>
      <span v-if="data" class="faint">{{ data.from }} to {{ data.to }}, {{ data.tz }}</span>
      <span v-if="loading && data" class="faint">Loading…</span>
    </div>

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
    <div v-else-if="totals.calls === 0" class="card empty" data-costs-empty>
      <h2>No calls</h2>
      <p>No agent call of the projects you may see falls into this range and these filters.</p>
    </div>
    <template v-else>
      <!-- Two columns on phones, three on tablets, six on desktop. -->
      <dl class="m-0 grid grid-cols-2 gap-3 tablet:grid-cols-3 desktop:grid-cols-6" data-costs-kpis>
        <div :class="kpi" data-kpi="cost">
          <dt :class="kpiLabel">Known cost</dt>
          <dd :class="[kpiValue, { italic: !totals.costKnown }]">{{ spendUSD(totals) }}</dd>
          <dd :class="kpiSub">
            <template v-if="change">{{ change }} vs the period before</template>
            <template v-else>no previous cost to compare</template>
          </dd>
        </div>
        <div v-if="month" :class="kpi" data-kpi="month">
          <dt :class="kpiLabel">Month to date</dt>
          <dd :class="[kpiValue, { italic: !month.current.costKnown }]">
            {{ month.current.costKnown ? cost(month.current.costUSD) : 'n/a' }}
          </dd>
          <dd :class="kpiSub">
            {{ month.previous.costKnown ? cost(month.previous.costUSD) : 'n/a' }} the same days last month<template v-if="monthChange">
              ({{ monthChange }})</template
            >
          </dd>
        </div>
        <div :class="kpi" data-kpi="calls">
          <dt :class="kpiLabel">Calls</dt>
          <dd :class="kpiValue">{{ totals.calls }}</dd>
          <dd v-if="totals.subagentCalls" :class="kpiSub">{{ totals.subagentCalls }} subagent</dd>
        </div>
        <div :class="kpi" :title="tokenBreakdown(totals)" data-kpi="tokens">
          <dt :class="kpiLabel">Metered tokens</dt>
          <dd :class="kpiValue">{{ tokenCount(spendTokens(totals)) }}</dd>
          <dd :class="kpiSub">{{ tokenCount(totals.inputTokens) }} in · {{ tokenCount(totals.outputTokens) }} out</dd>
        </div>
        <div :class="kpi" title="Cache reads of all input tokens (input, cache read and cache write)" data-kpi="cache">
          <dt :class="kpiLabel">Cache-read share</dt>
          <dd :class="kpiValue">{{ shareLabel(data.cacheReadShare) }}</dd>
          <dd v-if="data.previous.calls" :class="kpiSub">{{ shareLabel(data.previousCacheReadShare) }} the period before</dd>
        </div>
        <div :class="kpi" title="Calls without a reported or estimated cost are not in the sum" data-kpi="unknown">
          <dt :class="kpiLabel">Unknown cost</dt>
          <dd :class="kpiValue">{{ totals.unknownCalls }} {{ totals.unknownCalls === 1 ? 'call' : 'calls' }}</dd>
          <dd v-if="unmeteredNote(totals)" :class="kpiSub" title="Calls that report neither a cost nor tokens (cursor's model auto)">
            {{ unmeteredNote(totals) }}
          </dd>
        </div>
      </dl>
      <p v-if="basisNote(totals)" class="faint mt-2" :title="basisTitle" data-costs-basis>{{ basisNote(totals) }}</p>
      <p v-if="data.researchSince" class="faint mt-2" data-costs-research>
        Research counted since {{ dateLabel(data.researchSince) }}
      </p>
    </template>
  </div>
</template>
