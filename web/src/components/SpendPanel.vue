<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { getSpend, LoginRequiredError, NotFoundError } from '../api'
import { clockTime, duration, fullTime, tokenCount } from '../format'
import { useNow } from '../live'
import {
  callCost,
  childLabel,
  childScope,
  costSourceLabel,
  legacyNote,
  modelsLine,
  pctLabel,
  scopeFromQuery,
  shareLabel,
  shares,
  sortByCost,
  spendCrumbs,
  spendLocation,
  spendTokens,
  tokenBreakdown as tokenTitle,
  spendUSD,
  unknownNote,
  type SpendScope,
} from '../spend'
import type { Spend, SpendBreakdown } from '../types'

// SpendPanel is the Spend tab of a project or milestone stream: the
// scope's totals, its children one level down (project → milestone →
// issue → run → calls, the position in ?spendIssue=&spendTask=&spendRun=)
// and its calls by kind, role and model.
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
        <h3>{{ level.title }}</h3>
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
              <tr v-for="c in children" :key="c.key">
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
                <td class="num" :title="tokenTitle(c.totals)">{{ tokenCount(spendTokens(c.totals)) }}</td>
                <td
                  class="num"
                  :class="{ unknown: c.costSource !== 'reported' }"
                  :title="c.costSource === 'estimated' ? 'Estimated from tokens and the pricing table' : undefined"
                >
                  {{ callCost(c) }}
                </td>
                <td>{{ costSourceLabel(c.costSource) }}</td>
                <!-- The per-model bar of the call goes here (spend-model-bars). -->
                <td class="models">{{ modelsLine(c.models) }}</td>
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
                </td>
                <td :title="fullTime(c.at)">{{ clockTime(c.at, now) }}</td>
                <td class="num">
                  {{ c.totals.calls }}
                  <span v-if="c.totals.unknownCalls" class="faint" title="Calls without a known cost">
                    ({{ c.totals.unknownCalls }} n/a)
                  </span>
                </td>
                <td class="num" :title="tokenTitle(c.totals)">{{ tokenCount(spendTokens(c.totals)) }}</td>
                <td class="num" :class="{ unknown: !c.totals.costKnown }" :title="legacyNote(c.totals) || undefined">
                  {{ spendUSD(c.totals) }}
                </td>
                <td class="share-col">
                  <div class="share">
                    <span class="bar"><i :style="{ width: `${childShares.pct[i] ?? 0}%` }" /></span>
                    <span class="pct">{{ pctLabel(childShares.pct[i] ?? null) }}</span>
                  </div>
                </td>
                <!-- The per-model bar of a run goes here (spend-model-bars). -->
                <td class="models">{{ modelsLine(c.models) }}</td>
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
                  <td class="num" :title="tokenTitle(r.totals)">{{ tokenCount(spendTokens(r.totals)) }}</td>
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
}
.breakdowns {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 0 16px;
}
</style>
