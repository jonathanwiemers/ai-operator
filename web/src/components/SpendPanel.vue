<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getSpend, LoginRequiredError, NotFoundError } from '../api'
import { clockTime, duration, fullTime, tokenCount } from '../format'
import { useNow } from '../live'
import ModelBar from './ModelBar.vue'
import {
  avgCost,
  avgTokens,
  barColors,
  callCost,
  childLabel,
  companionNote,
  childScope,
  costSourceLabel,
  legacyNote,
  modelBars,
  modelSlots,
  modelsLine,
  pctLabel,
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
  unmeteredNote,
  type SpendScope,
  type SpendUnit,
} from '../spend'
import type { Spend, SpendBreakdown } from '../types'

// SpendPanel is the Spend tab of a project or milestone stream: the
// scope's totals, its children one level down (project → milestone →
// issue → run → calls, the position in ?spendIssue=&spendTask=&spendRun=)
// its calls by kind, role and model, and the tuning tables: review cost
// per round, implementer steps fresh vs resumed, with vs without subagents.
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
      e instanceof NotFoundError ? 'Spend needs the research database.' : e instanceof Error ? e.message : String(e)
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
const childShares = computed(() => shares(children.value.map((c) => c.totals)))

// The rows' per-model bars, in ?spendUnit= (tokens or USD), and the
// legend's subagent models with their colors.
const unit = computed(() => unitFromQuery(route.query))
const bars = computed(() => modelBars(children.value, unit.value))
const subagents = computed(() => [...modelSlots(children.value)])
const units: { unit: SpendUnit; label: string }[] = [
  { unit: 'tokens', label: 'tokens' },
  { unit: 'usd', label: 'USD' },
]

const breakdowns = computed(() => {
  const d = data.value
  if (!d) return []
  return [
    { title: 'By kind', rows: d.byKind },
    { title: 'By role', rows: d.byRole },
    { title: 'By model', rows: d.byModel },
  ].map((b) => {
    const rows = sortByCost(b.rows)
    return { ...b, rows, shares: shares(rows.map((r) => r.totals)) }
  })
})

// The tuning tables: what to set maxInlineDiff, stepSession and subagents
// by (docs/configuration.md).
const rounds = computed(() => reviewRounds(data.value?.byReviewRound ?? []))
const sessions = computed(() => sessionRows(data.value?.bySession ?? []))
const stepLines = computed(() => stepLinesLine(data.value?.stepLines))
const subagentRows = computed(() => subagentRoles(data.value?.bySubagents ?? []))

// level: what the children are, as the API groups them.
const level = computed(() => {
  if (atCalls.value) return { title: 'Calls', one: 'Call' }
  if (scope.value.issue) return { title: 'Runs', one: 'Run' }
  if (scope.value.milestone) return { title: 'Issues', one: 'Issue' }
  return { title: 'Milestones', one: 'Milestone' }
})

function rowKey(b: SpendBreakdown): string {
  return b.key || '—'
}
</script>

<template>
  <div class="spend">
    <nav class="crumbs" aria-label="Spend drill-down">
      <template v-for="(c, i) in crumbs" :key="i">
        <span v-if="i > 0" class="sep">›</span>
        <RouterLink v-if="i < crumbs.length - 1" :to="to(c.scope)">{{ c.label }}</RouterLink>
        <span v-else class="here" aria-current="page">{{ c.label }}</span>
      </template>
      <span v-if="loading && data" class="faint">Loading…</span>
    </nav>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-else-if="!data || !totals" class="empty">Loading…</div>
    <template v-else>
      <section class="card totals">
        <div class="usd">
          <strong :class="{ unknown: !totals.costKnown }">{{ spendUSD(totals) }}</strong>
          <span v-if="unknownNote(totals)" class="faint" title="Calls without a reported or estimated cost are not in the sum">
            {{ unknownNote(totals) }}
          </span>
          <span v-if="unmeteredNote(totals)" class="faint" title="Calls that report neither a cost nor tokens (cursor's model auto)">
            · {{ unmeteredNote(totals) }}
          </span>
        </div>
        <dl>
          <div>
            <dt>Input</dt>
            <dd>{{ tokenCount(totals.inputTokens) }}</dd>
          </div>
          <div>
            <dt>Output</dt>
            <dd>{{ tokenCount(totals.outputTokens) }}</dd>
          </div>
          <div>
            <dt>Cache read</dt>
            <dd>{{ tokenCount(totals.cacheReadTokens) }}</dd>
          </div>
          <div>
            <dt>Cache write</dt>
            <dd>{{ tokenCount(totals.cacheWriteTokens) }}</dd>
          </div>
          <div>
            <dt>Calls</dt>
            <dd>
              {{ totals.calls }}
              <span v-if="totals.subagentCalls" class="faint">· {{ totals.subagentCalls }} subagent</span>
            </dd>
          </div>
          <div>
            <dt>Duration</dt>
            <dd>{{ duration(totals.durationMS) }}</dd>
          </div>
          <div title="Cache reads of all input tokens (input, cache read and cache write)">
            <dt>Cache-read share</dt>
            <dd>{{ shareLabel(data.cacheReadShare) }}</dd>
          </div>
        </dl>
        <p v-if="legacyNote(totals)" class="note faint">{{ legacyNote(totals) }}</p>
      </section>

      <section class="section">
        <div class="head">
          <h3>{{ level.title }}</h3>
          <div v-if="children.length" class="legend">
            <span><i :style="{ background: barColors[0] }" />main model</span>
            <span v-for="[m, slot] in subagents" :key="m"><i :style="{ background: barColors[slot] }" />{{ m }} (subagent)</span>
          </div>
          <nav class="unit" aria-label="Bar unit">
            <RouterLink
              v-for="u in units"
              :key="u.unit"
              :to="{ query: unitQuery(route.query, u.unit) }"
              :class="{ on: unit === u.unit }"
              :aria-current="unit === u.unit ? 'true' : undefined"
              replace
            >
              {{ u.label }}
            </RouterLink>
          </nav>
        </div>
        <div v-if="!children.length" class="card empty">No agent calls recorded here.</div>
        <div v-else class="card table">
          <table v-if="atCalls" class="list">
            <thead>
              <tr>
                <th class="num">Seq</th>
                <th>Time</th>
                <th>Kind</th>
                <th>Role</th>
                <th>Engine / model</th>
                <th>Resumed</th>
                <th>Fallback from</th>
                <th class="num">Tokens</th>
                <th class="num">Cost</th>
                <th>Source</th>
                <th>Models</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(c, i) in children" :key="c.key">
                <td class="num">{{ c.key }}</td>
                <td :title="fullTime(c.at)">{{ clockTime(c.at, now) }}</td>
                <td>{{ c.kind }}</td>
                <td>{{ c.role || '—' }}</td>
                <td>
                  {{ c.engine }}<template v-if="c.model">/{{ c.model }}</template>
                  <span v-if="c.effort" class="faint"> · {{ c.effort }}</span>
                </td>
                <td>{{ c.resumed ? 'resumed' : '—' }}</td>
                <td>{{ c.fallbackFrom || '—' }}</td>
                <td class="num" :title="tokenTitle(c.totals)">{{ spendTokenLabel(c.totals) }}</td>
                <td
                  class="num"
                  :class="{ unknown: c.costSource !== 'reported' }"
                  :title="c.costSource === 'estimated' ? 'Estimated from tokens and the pricing table' : undefined"
                >
                  {{ callCost(c) }}
                </td>
                <td>{{ costSourceLabel(c.costSource) }}</td>
                <td class="models">
                  <ModelBar v-if="bars[i]" :bar="bars[i]!" />
                  <div>{{ modelsLine(c.models) }}</div>
                </td>
              </tr>
            </tbody>
          </table>
          <table v-else class="list">
            <thead>
              <tr>
                <th>{{ level.one }}</th>
                <th>First call</th>
                <th class="num">Calls</th>
                <th class="num">Tokens</th>
                <th class="num">Cost</th>
                <th class="share-col">Share of {{ childShares.basis }}</th>
                <th>Models</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(c, i) in children" :key="`${c.level}/${c.key}`">
                <td>
                  <RouterLink v-if="childScope(scope, c)" :to="to(childScope(scope, c)!)">
                    {{ childLabel(c, scope, milestones) }}
                  </RouterLink>
                  <span v-else>{{ childLabel(c, scope, milestones) }}</span>
                  <span v-if="companionNote(c)" class="companion-mark" :title="companionNote(c)">companion</span>
                </td>
                <td :title="fullTime(c.at)">{{ clockTime(c.at, now) }}</td>
                <td class="num">
                  {{ c.totals.calls }}
                  <span v-if="c.totals.unknownCalls" class="faint" title="Calls without a known cost">
                    ({{ c.totals.unknownCalls }} n/a)
                  </span>
                </td>
                <td class="num" :title="tokenTitle(c.totals)">{{ spendTokenLabel(c.totals) }}</td>
                <td class="num" :class="{ unknown: !c.totals.costKnown }" :title="legacyNote(c.totals) || undefined">
                  {{ spendUSD(c.totals) }}
                </td>
                <td class="share-col">
                  <div class="share">
                    <span class="bar"><i :style="{ width: `${childShares.pct[i] ?? 0}%` }" /></span>
                    <span class="pct">{{ pctLabel(childShares.pct[i] ?? null) }}</span>
                  </div>
                </td>
                <td class="models">
                  <ModelBar v-if="bars[i]" :bar="bars[i]!" />
                  <div>{{ modelsLine(c.models) }}</div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section v-if="totals.calls" class="breakdowns">
        <div v-for="b in breakdowns" :key="b.title" class="section">
          <h3>{{ b.title }}</h3>
          <div class="card table">
            <table class="list">
              <thead>
                <tr>
                  <th>{{ b.title.slice(3) }}</th>
                  <th class="num">Calls</th>
                  <th class="num">Tokens</th>
                  <th class="num">Cost</th>
                  <th class="share-col">Share</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(r, i) in b.rows" :key="r.key">
                  <td>{{ rowKey(r) }}</td>
                  <td class="num">{{ r.totals.calls }}</td>
                  <td class="num" :title="tokenTitle(r.totals)">{{ spendTokenLabel(r.totals) }}</td>
                  <td class="num" :class="{ unknown: !r.totals.costKnown }">{{ spendUSD(r.totals) }}</td>
                  <td class="share-col" :title="`Share of ${b.shares.basis}`">
                    <div class="share">
                      <span class="bar"><i :style="{ width: `${b.shares.pct[i] ?? 0}%` }" /></span>
                      <span class="pct">{{ pctLabel(b.shares.pct[i] ?? null) }}</span>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </section>

      <section v-if="totals.calls" class="breakdowns tuning">
        <div class="section">
          <h3>Review rounds</h3>
          <div v-if="rounds.length" class="card table">
            <table class="list">
              <thead>
                <tr>
                  <th>Round</th>
                  <th class="num">Calls</th>
                  <th class="num">Tokens</th>
                  <th class="num">Cost</th>
                  <th class="num">Per call</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in rounds" :key="r.key">
                  <td>{{ r.key }}</td>
                  <td class="num">{{ r.totals.calls }}</td>
                  <td class="num" :title="tokenTitle(r.totals)">{{ spendTokenLabel(r.totals) }}</td>
                  <td class="num" :class="{ unknown: !r.totals.costKnown }">{{ spendUSD(r.totals) }}</td>
                  <td class="num" :class="{ unknown: !r.totals.costKnown }">{{ avgCost(r.totals) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-else class="muted">No review calls here.</p>
        </div>
        <div class="section">
          <h3>Implementer steps: resumed vs fresh</h3>
          <div v-if="sessions.length" class="card table">
            <table class="list">
              <thead>
                <tr>
                  <th>Session</th>
                  <th class="num">Calls</th>
                  <th class="num">Tokens per call</th>
                  <th class="num">Cost</th>
                  <th class="num">Per call</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="r in sessions" :key="r.key">
                  <td>{{ r.key }}</td>
                  <td class="num">{{ r.totals.calls }}</td>
                  <td class="num" :title="tokenTitle(r.totals)">{{ tokenCount(avgTokens(r.totals)) }}</td>
                  <td class="num" :class="{ unknown: !r.totals.costKnown }">{{ spendUSD(r.totals) }}</td>
                  <td class="num" :class="{ unknown: !r.totals.costKnown }">{{ avgCost(r.totals) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-else class="muted">No implementer steps here.</p>
          <p
            v-if="stepLines"
            class="muted"
            title="Lines (added plus deleted) of the implement calls that left a step commit, and the step-size gate's nudges"
          >
            {{ stepLines }}
          </p>
        </div>
        <div class="section">
          <h3>With vs without subagents</h3>
          <div v-if="subagentRows.length" class="card table">
            <table class="list">
              <thead>
                <tr>
                  <th>Role</th>
                  <th>Subagents</th>
                  <th class="num">Calls</th>
                  <th class="num">Tokens per call</th>
                  <th class="num">Per call</th>
                  <th class="num" title="The subagent models' share of the calls' tokens">Subagent share</th>
                  <th class="num" title="Reads the read gate blocked, per call">Gate blocks per call</th>
                </tr>
              </thead>
              <tbody>
                <template v-for="g in subagentRows" :key="g.role">
                  <tr v-for="r in g.rows" :key="`${g.role}/${r.withSubagents}`">
                    <td>{{ r.withSubagents ? g.role || '—' : '' }}</td>
                    <td>{{ r.withSubagents ? 'with' : 'without' }}</td>
                    <td class="num">{{ r.totals.calls }}</td>
                    <td class="num" :title="tokenTitle(r.totals)">{{ tokenCount(avgTokens(r.totals)) }}</td>
                    <td class="num" :class="{ unknown: !r.totals.costKnown }">{{ avgCost(r.totals) }}</td>
                    <td class="num">{{ r.withSubagents ? subagentShare(r) : '' }}</td>
                    <td class="num">{{ gateBlocksPerCall(r) }}</td>
                  </tr>
                </template>
              </tbody>
            </table>
          </div>
          <p v-else class="muted">No call here ran a subagent.</p>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.crumbs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  margin-bottom: 12px;
}
.crumbs .sep {
  color: var(--text-3);
}
.crumbs .here {
  font-weight: 600;
}
.totals {
  padding: 14px 18px;
}
.usd {
  display: flex;
  align-items: baseline;
  gap: 10px;
}
.usd strong {
  font-size: 24px;
  font-variant-numeric: tabular-nums;
}
.companion-mark {
  margin-left: 6px;
  padding: 0 6px;
  border-radius: 999px;
  font-size: 11px;
  font-weight: 600;
  background: var(--tone-info-bg);
  color: var(--tone-info-fg);
}
.unknown {
  font-style: italic;
}
dl {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 24px;
  margin: 10px 0 0;
}
dt {
  font-size: 11.5px;
  color: var(--text-3);
}
dd {
  margin: 0;
  font-variant-numeric: tabular-nums;
}
.note {
  margin: 10px 0 0;
  font-size: 12.5px;
}
.section {
  margin-top: 20px;
}
.section h3 {
  margin: 0 0 8px;
  font-size: 14px;
}
.head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 16px;
}
.head .unit {
  margin-left: auto;
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 11.5px;
  color: var(--text-3);
}
.legend i {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 2px;
  margin-right: 4px;
  vertical-align: -1px;
}
.unit {
  display: flex;
  font-size: 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  overflow: hidden;
}
.unit a {
  padding: 2px 10px;
  color: var(--text-2);
  text-decoration: none;
}
.unit a + a {
  border-left: 1px solid var(--border);
}
.unit a.on {
  background: var(--surface-3);
  color: var(--text);
  font-weight: 600;
}
.table {
  padding: 0;
  overflow-x: auto;
}
.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
table.list th.num {
  text-align: right;
}
.share-col {
  min-width: 120px;
}
.share {
  display: flex;
  align-items: center;
  gap: 8px;
}
.bar {
  flex: 1;
  height: 6px;
  border-radius: 3px;
  background: var(--surface-3);
  overflow: hidden;
}
.bar i {
  display: block;
  height: 100%;
  background: var(--tone-progress-fg);
}
.pct {
  width: 3.5em;
  text-align: right;
  font-size: 12px;
  color: var(--text-2);
  font-variant-numeric: tabular-nums;
}
.models {
  font-size: 12px;
  color: var(--text-2);
  min-width: 180px;
}
.breakdowns {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 0 16px;
}
</style>
