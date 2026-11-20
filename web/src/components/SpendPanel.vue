<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getSpend, LoginRequiredError, NotFoundError } from '../client'
import { clockTime, duration, fullTime, tokenCount } from '../format'
import { useNow } from '../live'
import { measuredLabel, totalsLabel, totalsTooltip, writtenLabel } from '../lines'
import { per100Label } from '../metricsCode'
import DataList, { type DataListColumn } from '../ui/DataList.vue'
import ModelBar from './ModelBar.vue'
import StatTile from './StatTile.vue'
import {
  avgCost,
  kindLines,
  per100Notes,
  avgTokens,
  barColors,
  callCost,
  childLabel,
  companionNote,
  childScope,
  checkpointsLine,
  checkpointsNote,
  conflictRunLabel,
  costSourceLabel,
  kindModel,
  legacyNote,
  modelBars,
  modelSlots,
  modelLabel,
  modelsLine,
  pctLabel,
  perMillion,
  reviewRounds,
  scopeFromQuery,
  sessionRows,
  shareLabel,
  shares,
  sortByCost,
  spendCrumbs,
  spendLocation,
  spendTokenLabel,
  stepLinesLine,
  tokenBreakdown as tokenTitle,
  spendUSD,
  subagentRoles,
  gateBlocksPerCall,
  subagentShare,
  unitFromQuery,
  unitQuery,
  unknownNote,
  unmetered,
  unmeteredNote,
  basisNote,
  basisTitle,
  mixedBasis,
  type SpendScope,
  type SpendUnit,
} from '../spend'
import type { Spend, SpendBreakdown, SpendChild, SubagentBreakdown } from '../types'

// SpendPanel is the Metrics tab (once named Spend) of a project or
// milestone stream: the scope's totals with its written and merged lines
// and the cost per 100 merged production lines, its children one level down (project → milestone →
// issue → run → calls, the position in ?spendIssue=&spendTask=&spendRun=)
// its calls by kind, role and model, and the tuning tables: review cost
// per round, implementer steps fresh vs resumed, with vs without subagents.
//
// Utilities only (docs/contributing/web-ui.md, "Metrics tab"): the totals stack on phones, the
// tables are comparisons and keep their columns at every width, scrolling
// inside their own container (DataList with scroll), never the page.
const props = defineProps<{
  namespace: string
  name: string
  milestoneIid?: number
  milestones: { iid: number; title: string }[]
}>()

const route = useRoute()
const now = useNow()

const scope = computed(() => scopeFromQuery(props.milestoneIid, route.query))
const crumbs = computed(() =>
  spendCrumbs(scope.value, {
    project: props.name,
    milestone: props.milestones.find((m) => m.iid === scope.value.milestone)?.title,
  }),
)
const atCalls = computed(() => !!scope.value.task)

const data = ref<Spend | null>(null)
const loading = ref(false)
const error = ref('')
let seq = 0

async function load() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const sp = await getSpend(props.namespace, props.name, scope.value)
    if (mine !== seq) return
    data.value = sp
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return
    data.value = null
    error.value =
      e instanceof NotFoundError ? 'Metrics need the research database.' : e instanceof Error ? e.message : String(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

watch(
  () => [props.namespace, props.name, JSON.stringify(scope.value)],
  () => void load(),
  { immediate: true },
)

function to(s: SpendScope) {
  return spendLocation(props.namespace, props.name, s, route.query)
}

const totals = computed(() => data.value?.totals)
const children = computed(() => data.value?.children ?? [])
// Merged lines exist at project, milestone and issue scope; mergedLines is
// absent there while nothing merged ("—").
const hasMerged = computed(() => !scope.value.task)
const measured = computed(() => measuredLabel(data.value?.mergedLines))
const per100Note = computed(() => (totals.value ? per100Notes(totals.value) : ''))
const per100Title =
  'The cost of the merged issues’ calls per 100 merged production lines (added plus deleted, companions included); ≤ when some diff came without content'
const childShares = computed(() => shares(children.value.map((c) => c.totals)))

// The rows' per-model bars, in ?spendUnit= (tokens or USD), and the
// legend's subagent models with their colors.
const unit = computed(() => unitFromQuery(route.query))
const bars = computed(() => modelBars(children.value, unit.value))
const subagents = computed(() => [...modelSlots(children.value)])
const units: { value: SpendUnit; label: string }[] = [
  { value: 'tokens', label: 'tokens' },
  { value: 'usd', label: 'USD' },
]
// The unit is a local switch (decision 8), kept in the query.
const router = useRouter()
function setUnit(v: string | number) {
  const u = units.find((x) => x.value === v)?.value
  if (u) void router.replace({ query: unitQuery(route.query, u) })
}

// childRows: the children with their position, for the bars and shares.
type ChildRow = SpendChild & { i: number }
const childRows = computed<ChildRow[]>(() => children.value.map((c, i) => ({ ...c, i })))
const childKey = (c: ChildRow) => `${c.level}/${c.key}`

const breakdowns = computed(() => {
  const d = data.value
  if (!d) return []
  return [
    // By kind has the written lines of the implementer kinds.
    { title: 'By kind', rows: d.byKind, label: rowKey, lines: true },
    { title: 'By role', rows: d.byRole, label: rowKey, lines: false },
    // cursor's auto as "cursor auto (unmetered)".
    { title: 'By model', rows: d.byModel, label: (b: SpendBreakdown) => modelLabel(rowKey(b), unmetered(b.totals)), lines: false },
  ].map((b) => {
    const sorted = sortByCost(b.rows)
    const pct = shares(sorted.map((r) => r.totals))
    return {
      ...b,
      basis: pct.basis,
      columns: [
        { key: 'key', label: b.title.slice(3) },
        { key: 'calls', label: 'Calls', numeric: true },
        { key: 'tokens', label: 'Tokens', numeric: true },
        { key: 'cost', label: 'Cost', numeric: true },
        ...(b.lines ? [{ key: 'lines', label: 'Lines', numeric: true }] : []),
        { key: 'share', label: 'Share' },
      ] as DataListColumn[],
      rows: sorted.map((r, i) => ({ ...r, pct: pct.pct[i] ?? null })),
    }
  })
})

// The tuning tables: what to set maxInlineDiff, stepSession and subagents
// by (docs/reference/agents-and-models.md).
const rounds = computed(() => reviewRounds(data.value?.byReviewRound ?? []))
const sessions = computed(() => sessionRows(data.value?.bySession ?? []))
const stepLines = computed(() => stepLinesLine(data.value?.stepLines))
const checkpoints = computed(() => checkpointsLine(data.value?.byCheckpoint ?? []))
// subagentRows: one row per role and with/without, the role named on the
// first of its rows.
type SubagentRow = SubagentBreakdown & { label: string }
const subagentRows = computed<SubagentRow[]>(() =>
  subagentRoles(data.value?.bySubagents ?? []).flatMap((g) =>
    g.rows.map((r) => ({ ...r, label: r.withSubagents ? g.role || '—' : '' })),
  ),
)
const subagentKey = (r: SubagentRow) => `${r.role}/${r.withSubagents}`
const kindModels = computed(() => sortByCost(data.value?.byKindModel ?? []))
const conflictRuns = computed(() => sortByCost(data.value?.conflictRuns ?? []))

// level: what the children are, as the API groups them.
const level = computed(() => {
  if (atCalls.value) return { title: 'Calls', one: 'Call' }
  if (scope.value.issue) return { title: 'Runs', one: 'Run' }
  if (scope.value.milestone) return { title: 'Issues', one: 'Issue' }
  return { title: 'Milestones', one: 'Milestone' }
})

const callColumns: DataListColumn[] = [
  { key: 'key', label: 'Seq', numeric: true },
  { key: 'at', label: 'Time' },
  { key: 'kind', label: 'Kind' },
  { key: 'role', label: 'Role' },
  { key: 'engine', label: 'Engine / model' },
  { key: 'resumed', label: 'Resumed' },
  { key: 'fallbackFrom', label: 'Fallback from' },
  { key: 'tokens', label: 'Tokens', numeric: true },
  { key: 'cost', label: 'Cost', numeric: true },
  { key: 'source', label: 'Source' },
  { key: 'models', label: 'Models' },
]
const childColumns = computed<DataListColumn[]>(() => [
  { key: 'label', label: level.value.one },
  { key: 'at', label: 'First call' },
  { key: 'calls', label: 'Calls', numeric: true },
  { key: 'tokens', label: 'Tokens', numeric: true },
  { key: 'cost', label: 'Cost', numeric: true },
  { key: 'share', label: `Share of ${childShares.value.basis}` },
  { key: 'models', label: 'Models' },
])
const kindModelColumns: DataListColumn[] = [
  { key: 'kind', label: 'Kind' },
  { key: 'model', label: 'Model' },
  { key: 'calls', label: 'Calls', numeric: true },
  { key: 'output', label: 'Output', numeric: true },
  { key: 'cacheWrite', label: 'Cache write', numeric: true },
  { key: 'cost', label: 'Cost', numeric: true },
  { key: 'perMillion', label: '$ per 1M tokens', numeric: true },
]
const conflictColumns: DataListColumn[] = [
  { key: 'run', label: 'Run' },
  { key: 'calls', label: 'Calls', numeric: true },
  { key: 'output', label: 'Output', numeric: true },
  { key: 'cacheWrite', label: 'Cache write', numeric: true },
  { key: 'cost', label: 'Cost', numeric: true },
  { key: 'perMillion', label: '$ per 1M tokens', numeric: true },
]
const roundColumns: DataListColumn[] = [
  { key: 'key', label: 'Round' },
  { key: 'calls', label: 'Calls', numeric: true },
  { key: 'tokens', label: 'Tokens', numeric: true },
  { key: 'cost', label: 'Cost', numeric: true },
  { key: 'perCall', label: 'Per call', numeric: true },
]
const sessionColumns: DataListColumn[] = [
  { key: 'key', label: 'Session' },
  { key: 'calls', label: 'Calls', numeric: true },
  { key: 'tokensPerCall', label: 'Tokens per call', numeric: true },
  { key: 'cost', label: 'Cost', numeric: true },
  { key: 'perCall', label: 'Per call', numeric: true },
]
const subagentColumns: DataListColumn[] = [
  { key: 'label', label: 'Role' },
  { key: 'with', label: 'Subagents' },
  { key: 'calls', label: 'Calls', numeric: true },
  { key: 'tokensPerCall', label: 'Tokens per call', numeric: true },
  { key: 'perCall', label: 'Per call', numeric: true },
  { key: 'share', label: 'Subagent share', numeric: true },
  { key: 'gate', label: 'Gate blocks per call', numeric: true },
]

function rowKey(b: SpendBreakdown): string {
  return b.key || '—'
}

// A section's heading, a card around each table, and the drill-down links
// (crumbs, unit, a row's level), 44 px high on phones.
const heading = 'mb-2 text-sm font-semibold'
const tableCard = 'card overflow-hidden'
const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
const mark = 'ms-1.5 rounded-full px-1.5 text-[11px] font-semibold'
</script>

<template>
  <div>
    <nav class="mb-3 flex flex-wrap items-center gap-x-1.5 text-[13px]" aria-label="Spend drill-down" data-spend-crumbs>
      <template v-for="(c, i) in crumbs" :key="i">
        <span v-if="i > 0" class="text-(--text-3)">›</span>
        <RouterLink v-if="i < crumbs.length - 1" :to="to(c.scope)" :class="tap">{{ c.label }}</RouterLink>
        <span v-else :class="tap" class="font-semibold" aria-current="page">{{ c.label }}</span>
      </template>
      <span v-if="loading && data" class="faint">Loading…</span>
      <RouterLink
        :to="{ path: '/metrics', query: { project: `${namespace}/${name}` } }"
        :class="tap"
        class="ms-auto"
        data-spend-costs
      >
        Metrics across projects
      </RouterLink>
    </nav>

    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <div v-else-if="!data || !totals" class="empty">Loading…</div>
    <template v-else>
      <section data-spend-totals>
        <!-- The cost first, then the tokens, calls and time, then the lines
             next to the cost: written (all calls), merged and the cost per
             100 merged production lines (project, milestone and issue scope;
             task and run scopes have no merged lines). -->
        <div class="stat-tiles">
          <StatTile label="Spend">
            <strong :class="{ italic: !totals.costKnown }">{{ spendUSD(totals) }}</strong>
            <template v-if="unknownNote(totals) || unmeteredNote(totals) || basisNote(totals)" #sub>
              <span v-if="unknownNote(totals)" title="Calls without a reported or estimated cost are not in the sum">{{ unknownNote(totals) }}</span>
              <span v-if="unmeteredNote(totals)" title="Calls that report neither a cost nor tokens (cursor's model auto)">
                · {{ unmeteredNote(totals) }}
              </span>
              <span v-if="basisNote(totals)" :title="basisTitle">· {{ basisNote(totals) }}</span>
            </template>
          </StatTile>
          <StatTile label="Input" :value="tokenCount(totals.inputTokens)" />
          <StatTile label="Output" :value="tokenCount(totals.outputTokens)" />
          <StatTile label="Cache read" :value="tokenCount(totals.cacheReadTokens)" />
          <StatTile label="Cache write" :value="tokenCount(totals.cacheWriteTokens)" />
          <StatTile label="Calls" :value="totals.calls" :sub="totals.subagentCalls ? `${totals.subagentCalls} subagent` : undefined" />
          <StatTile label="Checkpoints" :value="totals.checkpoints" :title="[checkpoints, checkpointsNote].filter(Boolean).join(' · ')" />
          <StatTile label="Duration" :value="duration(totals.durationMS)" />
          <StatTile
            label="Cache-read share"
            :value="shareLabel(data.cacheReadShare)"
            title="Cache reads of all input tokens (input, cache read and cache write)"
          />
        </div>
        <div class="stat-tiles mt-3" data-spend-lines>
          <StatTile label="Written" title="Written lines: the step commits of the implementer calls, rewrites and fix rounds included">
            <span data-lines="written">{{ writtenLabel(totals) }}</span>
          </StatTile>
          <template v-if="hasMerged">
            <StatTile label="Merged production" :sub="measured || undefined" :title="totalsTooltip(data.mergedLines)">
              <span data-lines="merged">{{ totalsLabel(data.mergedLines) }}</span>
            </StatTile>
            <StatTile label="Cost per 100 merged production lines" :sub="per100Note || undefined" :title="per100Title">
              <span data-lines="per100">{{ per100Label(data.mergedLines) }}</span>
            </StatTile>
          </template>
        </div>
        <p v-if="legacyNote(totals)" class="faint mt-2.5 text-[12.5px]">{{ legacyNote(totals) }}</p>
      </section>

      <section class="mt-5">
        <div class="mb-2 flex flex-wrap items-center gap-x-4 gap-y-1">
          <h3 class="text-sm font-semibold">{{ level.title }}</h3>
          <div v-if="children.length" class="flex flex-wrap gap-3 text-[11.5px] text-(--text-3)">
            <span><i class="me-1 inline-block size-2 rounded-xs align-[-1px]" :style="{ background: barColors[0] }" />main model</span>
            <span v-for="[m, slot] in subagents" :key="m">
              <i class="me-1 inline-block size-2 rounded-xs align-[-1px]" :style="{ background: barColors[slot] }" />{{ m }} (subagent)
            </span>
          </div>
          <UTabs
            :model-value="unit"
            :items="units"
            variant="pill"
            size="sm"
            :content="false"
            class="ms-auto"
            aria-label="Bar unit"
            data-spend-unit
            @update:model-value="setUnit"
          />
        </div>
        <UEmpty v-if="!children.length" icon="i-lucide-receipt" title="No agent calls recorded here." size="sm" />
        <div v-else :class="tableCard">
          <DataList v-if="atCalls" :columns="callColumns" :rows="childRows" :row-key="childKey" scroll data-spend="children">
            <template #cell-at="{ row }"><span :title="fullTime(row.at)">{{ clockTime(row.at, now) }}</span></template>
            <template #cell-role="{ row }">{{ row.role || '—' }}</template>
            <template #cell-engine="{ row }">
              {{ row.engine }}<template v-if="row.model">/{{ row.model }}</template>
              <span v-if="row.effort" class="faint"> · {{ row.effort }}</span>
            </template>
            <template #cell-resumed="{ row }">{{ row.resumed ? 'resumed' : '—' }}</template>
            <template #cell-fallbackFrom="{ row }">{{ row.fallbackFrom || '—' }}</template>
            <template #cell-tokens="{ row }"><span :title="tokenTitle(row.totals)">{{ spendTokenLabel(row.totals) }}</span></template>
            <template #cell-cost="{ row }">
              <span
                :class="{ italic: row.costSource !== 'reported' }"
                :title="row.costSource === 'estimated' ? 'Estimated from tokens and the pricing table' : undefined"
                >{{ callCost(row) }}</span
              >
            </template>
            <template #cell-source="{ row }">{{ costSourceLabel(row.costSource) }}</template>
            <template #cell-models="{ row }">
              <div class="w-48 text-xs text-(--text-2)">
                <ModelBar v-if="bars[row.i]" :bar="bars[row.i]!" />
                <div class="whitespace-normal">{{ modelsLine(row.models) }}</div>
              </div>
            </template>
          </DataList>
          <DataList v-else :columns="childColumns" :rows="childRows" :row-key="childKey" scroll data-spend="children">
            <template #cell-label="{ row }">
              <!-- A long title wraps within 16rem, so the table is not as wide as the title. -->
              <span class="inline-block max-w-64 whitespace-normal">
                <RouterLink v-if="childScope(scope, row)" :to="to(childScope(scope, row)!)" :class="tap">
                  {{ childLabel(row, scope, milestones) }}
                </RouterLink>
                <span v-else>{{ childLabel(row, scope, milestones) }}</span>
                <span
                  v-if="companionNote(row)"
                  :class="mark"
                  class="bg-(--tone-info-bg) text-(--tone-info-fg)"
                  :title="companionNote(row)"
                  >companion</span
                >
                <span
                  v-if="row.level === 'milestone' && basisNote(row.totals)"
                  :class="mark"
                  class="bg-(--tone-warning-bg) text-(--tone-warning-fg)"
                  :title="basisTitle"
                >
                  {{ mixedBasis(row.totals) ? 'mixed basis' : 'old basis' }}
                </span>
              </span>
            </template>
            <template #cell-at="{ row }"><span :title="fullTime(row.at)">{{ clockTime(row.at, now) }}</span></template>
            <template #cell-calls="{ row }">
              {{ row.totals.calls }}
              <span v-if="row.totals.unknownCalls" class="faint" title="Calls without a known cost">({{ row.totals.unknownCalls }} n/a)</span>
            </template>
            <template #cell-tokens="{ row }"><span :title="tokenTitle(row.totals)">{{ spendTokenLabel(row.totals) }}</span></template>
            <template #cell-cost="{ row }">
              <span :class="{ italic: !row.totals.costKnown }" :title="legacyNote(row.totals) || undefined">{{ spendUSD(row.totals) }}</span>
            </template>
            <template #cell-share="{ row }">
              <div class="flex w-32 items-center gap-2">
                <span class="h-1.5 flex-1 overflow-hidden rounded-[3px] bg-(--surface-3)">
                  <i class="block h-full bg-(--tone-progress-fg)" :style="{ width: `${childShares.pct[row.i] ?? 0}%` }" />
                </span>
                <span class="w-[3.5em] text-right text-xs text-(--text-2) tabular-nums">{{ pctLabel(childShares.pct[row.i] ?? null) }}</span>
              </div>
            </template>
            <template #cell-models="{ row }">
              <div class="w-48 text-xs text-(--text-2)">
                <ModelBar v-if="bars[row.i]" :bar="bars[row.i]!" />
                <div class="whitespace-normal">{{ modelsLine(row.models) }}</div>
              </div>
            </template>
          </DataList>
        </div>
      </section>

      <section v-if="totals.calls" class="grid grid-cols-[repeat(auto-fit,minmax(260px,1fr))] gap-x-4">
        <div v-for="b in breakdowns" :key="b.title" class="mt-5 min-w-0">
          <h3 :class="heading">{{ b.title }}</h3>
          <div :class="tableCard">
            <DataList :columns="b.columns" :rows="b.rows" :row-key="(r) => r.key" scroll :data-spend="b.title.slice(3)">
              <template #cell-key="{ row }">{{ b.label(row) }}</template>
              <template #cell-calls="{ row }">{{ row.totals.calls }}</template>
              <template #cell-tokens="{ row }"><span :title="tokenTitle(row.totals)">{{ spendTokenLabel(row.totals) }}</span></template>
              <template #cell-cost="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ spendUSD(row.totals) }}</span></template>
              <template #cell-lines="{ row }"><span title="Written lines: the step commits of the calls">{{ kindLines(row) }}</span></template>
              <template #cell-share="{ row }">
                <div class="flex w-28 items-center gap-2" :title="`Share of ${b.basis}`">
                  <span class="h-1.5 flex-1 overflow-hidden rounded-[3px] bg-(--surface-3)">
                    <i class="block h-full bg-(--tone-progress-fg)" :style="{ width: `${row.pct ?? 0}%` }" />
                  </span>
                  <span class="w-[3.5em] text-right text-xs text-(--text-2) tabular-nums">{{ pctLabel(row.pct) }}</span>
                </div>
              </template>
            </DataList>
          </div>
        </div>
      </section>

      <section v-if="totals.calls" class="mt-5">
        <h3 :class="heading">By kind and model</h3>
        <div :class="tableCard">
          <DataList :columns="kindModelColumns" :rows="kindModels" :row-key="(r) => r.key" scroll data-spend="kind-model">
            <template #cell-kind="{ row }">{{ kindModel(row.key).kind || '—' }}</template>
            <template #cell-model="{ row }">{{ modelLabel(kindModel(row.key).model || '—', unmetered(row.totals)) }}</template>
            <template #cell-calls="{ row }">{{ row.totals.calls }}</template>
            <template #cell-output="{ row }"><span :title="tokenTitle(row.totals)">{{ tokenCount(row.totals.outputTokens) }}</span></template>
            <template #cell-cacheWrite="{ row }">{{ tokenCount(row.totals.cacheWriteTokens) }}</template>
            <template #cell-cost="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ spendUSD(row.totals) }}</span></template>
            <template #cell-perMillion="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ perMillion(row.totals) }}</span></template>
          </DataList>
        </div>
        <p class="faint mt-1.5 text-xs">$ per 1M tokens: the cost per million tokens of all types (input, output, cache read and write).</p>
      </section>

      <section v-if="conflictRuns.length" class="mt-5">
        <h3 :class="heading">Conflict calls per run</h3>
        <div :class="tableCard">
          <DataList :columns="conflictColumns" :rows="conflictRuns" :row-key="(r) => r.key" scroll data-spend="conflict">
            <template #cell-run="{ row }">{{ conflictRunLabel(row.key) }}</template>
            <template #cell-calls="{ row }">
              {{ row.totals.calls }}
              <span v-if="row.totals.calls > 1" class="faint">repeat</span>
            </template>
            <template #cell-output="{ row }"><span :title="tokenTitle(row.totals)">{{ tokenCount(row.totals.outputTokens) }}</span></template>
            <template #cell-cacheWrite="{ row }">{{ tokenCount(row.totals.cacheWriteTokens) }}</template>
            <template #cell-cost="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ spendUSD(row.totals) }}</span></template>
            <template #cell-perMillion="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ perMillion(row.totals) }}</span></template>
          </DataList>
        </div>
        <p class="faint mt-1.5 text-xs">More than one call (repeat): the run resolved conflicts repeatedly.</p>
      </section>

      <section v-if="totals.calls" class="grid grid-cols-[repeat(auto-fit,minmax(260px,1fr))] gap-x-4">
        <div class="mt-5 min-w-0">
          <h3 :class="heading">Review rounds</h3>
          <div v-if="rounds.length" :class="tableCard">
            <DataList :columns="roundColumns" :rows="rounds" :row-key="(r) => r.key" scroll data-spend="rounds">
              <template #cell-calls="{ row }">{{ row.totals.calls }}</template>
              <template #cell-tokens="{ row }"><span :title="tokenTitle(row.totals)">{{ spendTokenLabel(row.totals) }}</span></template>
              <template #cell-cost="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ spendUSD(row.totals) }}</span></template>
              <template #cell-perCall="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ avgCost(row.totals) }}</span></template>
            </DataList>
          </div>
          <p v-else class="muted">No review calls here.</p>
        </div>
        <div class="mt-5 min-w-0">
          <h3 :class="heading">Implementer steps: resumed vs fresh</h3>
          <div v-if="sessions.length" :class="tableCard">
            <DataList :columns="sessionColumns" :rows="sessions" :row-key="(r) => r.key" scroll data-spend="sessions">
              <template #cell-calls="{ row }">{{ row.totals.calls }}</template>
              <template #cell-tokensPerCall="{ row }"><span :title="tokenTitle(row.totals)">{{ tokenCount(avgTokens(row.totals)) }}</span></template>
              <template #cell-cost="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ spendUSD(row.totals) }}</span></template>
              <template #cell-perCall="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ avgCost(row.totals) }}</span></template>
            </DataList>
          </div>
          <p v-else class="muted">No implementer steps here.</p>
          <!-- What the lines are is said here, not in a title: a phone has no hover. -->
          <p v-if="stepLines" class="muted mt-1.5">
            {{ stepLines }}
            <span class="faint text-xs">(lines added plus deleted of the implement calls that left a step commit, and the step-size gate's nudges)</span>
          </p>
        </div>
        <div class="mt-5 min-w-0">
          <h3 :class="heading">With vs without subagents</h3>
          <div v-if="subagentRows.length" :class="tableCard">
            <DataList :columns="subagentColumns" :rows="subagentRows" :row-key="subagentKey" scroll data-spend="subagents">
              <template #cell-with="{ row }">{{ row.withSubagents ? 'with' : 'without' }}</template>
              <template #cell-calls="{ row }">{{ row.totals.calls }}</template>
              <template #cell-tokensPerCall="{ row }"><span :title="tokenTitle(row.totals)">{{ tokenCount(avgTokens(row.totals)) }}</span></template>
              <template #cell-perCall="{ row }"><span :class="{ italic: !row.totals.costKnown }">{{ avgCost(row.totals) }}</span></template>
              <template #cell-share="{ row }">{{ row.withSubagents ? subagentShare(row) : '' }}</template>
              <template #cell-gate="{ row }">{{ gateBlocksPerCall(row) }}</template>
            </DataList>
          </div>
          <p v-if="subagentRows.length" class="faint mt-1.5 text-xs">
            Subagent share: the subagent models' share of the calls' tokens. Gate blocks per call: reads the read gate blocked, per call.
          </p>
          <p v-else class="muted">No call here ran a subagent.</p>
        </div>
      </section>
    </template>
  </div>
</template>
