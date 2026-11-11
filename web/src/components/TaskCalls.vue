<script setup lang="ts">
import { computed, ref, toRaw, watch } from 'vue'
import { getSpend, LoginRequiredError } from '../api'
import { clockTime, duration, fullTime, shortSHA, tokenCount } from '../format'
import { useNow } from '../live'
import {
  callCost,
  callLines,
  callNudges,
  checkpointsLine,
  checkpointsNote,
  costSourceLabel,
  legacyNote,
  runNumber,
  runsNewestFirst,
  spendHidden,
  spendTokens,
  spendUSD,
  tokenBreakdown,
  unknownNote,
} from '../spend'
import type { Spend, SpendChild } from '../types'
import Badge from './Badge.vue'
import ContextChart from './ContextChart.vue'
import DataList, { type DataListColumn } from '../ui/DataList.vue'

// TaskCalls is the "Agent calls" section of the Task page: the task's
// totals, its runs newest first and, per expanded run, its calls (loaded
// on demand). It hides itself when research is off (404 / 503).
const props = defineProps<{ namespace: string; name: string; task: string }>()
const now = useNow()

const data = ref<Spend | null>(null)
const hidden = ref(false)
const error = ref('')
const open = ref<Record<string, { loading: boolean; error: string; calls: SpendChild[] }>>({})
let seq = 0

const runs = computed(() => runsNewestFirst(data.value?.children ?? []))
const checkpoints = computed(() => checkpointsLine(data.value?.byCheckpoint ?? []))

const runColumns: DataListColumn[] = [
  { key: 'run', label: 'Run' },
  { key: 'at', label: 'First call' },
  { key: 'calls', label: 'Calls', numeric: true },
  { key: 'tokens', label: 'Tokens', numeric: true },
  { key: 'cost', label: 'Cost', numeric: true },
]
const callColumns: DataListColumn[] = [
  { key: 'key', label: '#', numeric: true },
  { key: 'at', label: 'Time' },
  { key: 'kind', label: 'Kind' },
  { key: 'role', label: 'Role' },
  { key: 'engine', label: 'Engine / model' },
  { key: 'resumed', label: 'Resumed' },
  { key: 'lines', label: 'Lines', numeric: true },
  { key: 'in', label: 'In', numeric: true },
  { key: 'out', label: 'Out', numeric: true },
  { key: 'cacheRead', label: 'Cache read', numeric: true },
  { key: 'cacheWrite', label: 'Cache write', numeric: true },
  { key: 'duration', label: 'Duration', numeric: true },
  { key: 'cost', label: 'Cost', numeric: true },
]

async function load() {
  const mine = ++seq
  open.value = {}
  try {
    const sp = await getSpend(props.namespace, props.name, { task: props.task })
    if (mine !== seq) return
    data.value = sp
    hidden.value = false
    error.value = ''
  } catch (e) {
    if (mine !== seq || e instanceof LoginRequiredError) return
    if (spendHidden(e)) hidden.value = true
    else error.value = e instanceof Error ? e.message : String(e)
  }
}
watch(() => [props.namespace, props.name, props.task], load, { immediate: true })

const none = { loading: false, error: '', calls: [] as SpendChild[] }
const st = (k: string) => open.value[k] ?? none

async function toggle(r: SpendChild) {
  if (open.value[r.key]) {
    delete open.value[r.key]
    return
  }
  const pending = { loading: true, error: '', calls: [] as SpendChild[] }
  open.value[r.key] = pending
  let next = { loading: false, error: '', calls: [] as SpendChild[] }
  try {
    const sp = await getSpend(props.namespace, props.name, { task: props.task, run: runNumber(r) })
    next = { ...next, calls: sp.children }
  } catch (e) {
    next = { ...next, error: e instanceof Error ? e.message : String(e) }
  }
  // Collapsed or reset by load() meanwhile: drop the answer.
  if (toRaw(open.value[r.key]) === pending) open.value[r.key] = next
}
</script>

<template>
  <section v-if="!hidden && (data || error)" class="section">
    <h3>Agent calls</h3>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <template v-if="data">
      <div v-if="data.totals.calls || data.totals.checkpoints" class="card mb-2.5 flex flex-wrap gap-x-4.5 gap-y-1.5 px-4 py-2.5">
        <span><strong>{{ spendUSD(data.totals) }}</strong></span>
        <span :title="tokenBreakdown(data.totals)">{{ tokenCount(spendTokens(data.totals)) }} tokens</span>
        <span>{{ data.totals.calls }} calls</span>
        <span v-if="data.totals.durationMS">{{ duration(data.totals.durationMS) }}</span>
        <span v-if="unknownNote(data.totals)" class="faint">{{ unknownNote(data.totals) }}</span>
        <span v-if="legacyNote(data.totals)" class="faint">{{ legacyNote(data.totals) }}</span>
        <span v-if="checkpoints" :title="checkpointsNote">💾 {{ checkpoints }}</span>
      </div>
      <div v-if="!runs.length" class="card empty">No agent calls recorded for this task.</div>
      <div v-else class="card">
        <DataList :columns="runColumns" :rows="runs" :row-key="(r) => r.key" :expanded="(r) => !!open[r.key]">
          <template #cell-run="{ row: r }">
            <!-- The button covers its row, so the row toggles; the hints sit above it (z-1). 44 px high on a phone. -->
            <button
              type="button"
              class="min-h-11 cursor-pointer text-left font-semibold text-(--link) after:absolute after:inset-0 tablet:min-h-0"
              :aria-expanded="!!open[r.key]"
              @click="toggle(r)"
            >
              {{ open[r.key] ? '▾' : '▸' }} Run {{ runNumber(r) || r.label }}
            </button>
          </template>
          <template #cell-at="{ row: r }"><span class="relative z-1" :title="fullTime(r.at)">{{ clockTime(r.at, now) }}</span></template>
          <template #cell-calls="{ row: r }">{{ r.totals.calls }}</template>
          <template #cell-tokens="{ row: r }">
            <span class="relative z-1" :title="tokenBreakdown(r.totals)">{{ tokenCount(spendTokens(r.totals)) }}</span>
          </template>
          <template #cell-cost="{ row: r }">
            <span class="relative z-1" :class="{ 'text-(--text-3)': !r.totals.costKnown }" :title="legacyNote(r.totals) || undefined">
              {{ spendUSD(r.totals) }}
            </span>
          </template>
          <template #detail="{ row: r }">
            <div v-if="st(r.key).loading" class="faint">Loading…</div>
            <div v-else-if="st(r.key).error" class="banner danger">{{ st(r.key).error }}</div>
            <div v-else-if="!st(r.key).calls.length" class="faint">
              No calls recorded for this run (cost only, from before token tracking).
            </div>
            <template v-else>
              <ContextChart :calls="st(r.key).calls" />
              <DataList :columns="callColumns" :rows="st(r.key).calls" :row-key="(c) => c.key">
                <template #cell-at="{ row: c }"><span :title="fullTime(c.at)">{{ clockTime(c.at, now) }}</span></template>
                <template #cell-kind="{ row: c }">
                  {{ c.kind }}
                  <Badge
                    v-if="c.checkpoint"
                    tone="warning"
                    :title="`Its end saved the checkpoint commit ${shortSHA(c.checkpointSHA)}`"
                  >
                    💾 {{ c.checkpoint }}
                  </Badge>
                </template>
                <template #cell-role="{ row: c }">{{ c.role || '—' }}</template>
                <template #cell-engine="{ row: c }">
                  {{ c.engine }}<template v-if="c.model">/{{ c.model }}</template>
                  <span v-if="c.fallbackFrom" class="faint"> · fallback from {{ c.fallbackFrom }}</span>
                  <span v-if="c.readGateBlocks" class="faint" title="Reads the read gate blocked">
                    · {{ c.readGateBlocks }} gate {{ c.readGateBlocks === 1 ? 'block' : 'blocks' }}
                  </span>
                </template>
                <template #cell-resumed="{ row: c }">{{ c.resumed ? 'resumed' : '—' }}</template>
                <template #cell-lines="{ row: c }">
                  {{ callLines(c) || '—' }}
                  <span
                    v-if="callNudges(c)"
                    class="faint"
                    title="How often the step-size gate told the implementer to close its step"
                  >
                    · {{ callNudges(c) }}
                  </span>
                </template>
                <template #cell-in="{ row: c }">{{ tokenCount(c.totals.inputTokens) }}</template>
                <template #cell-out="{ row: c }">{{ tokenCount(c.totals.outputTokens) }}</template>
                <template #cell-cacheRead="{ row: c }">{{ tokenCount(c.totals.cacheReadTokens) }}</template>
                <template #cell-cacheWrite="{ row: c }">{{ tokenCount(c.totals.cacheWriteTokens) }}</template>
                <template #cell-duration="{ row: c }">{{ c.totals.durationMS ? duration(c.totals.durationMS) : '—' }}</template>
                <template #cell-cost="{ row: c }">
                  <span
                    :class="{ 'text-(--text-3)': c.costSource !== 'reported' }"
                    :title="c.costSource === 'estimated' ? 'Estimated from tokens and the pricing table' : costSourceLabel(c.costSource)"
                  >
                    {{ callCost(c) }}
                  </span>
                </template>
              </DataList>
            </template>
          </template>
        </DataList>
      </div>
    </template>
  </section>
</template>
