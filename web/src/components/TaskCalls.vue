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
      <div v-if="data.totals.calls || data.totals.checkpoints" class="card totals">
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
        <table class="list">
          <thead>
            <tr>
              <th>Run</th>
              <th>First call</th>
              <th class="num">Calls</th>
              <th class="num">Tokens</th>
              <th class="num">Cost</th>
            </tr>
          </thead>
          <tbody v-for="r in runs" :key="r.key">
            <tr class="run" @click="toggle(r)">
              <td>
                <button class="link" :aria-expanded="!!open[r.key]" @click.stop="toggle(r)">
                  {{ open[r.key] ? '▾' : '▸' }} Run {{ runNumber(r) || r.label }}
                </button>
              </td>
              <td :title="fullTime(r.at)">{{ clockTime(r.at, now) }}</td>
              <td class="num">{{ r.totals.calls }}</td>
              <td class="num" :title="tokenBreakdown(r.totals)">{{ tokenCount(spendTokens(r.totals)) }}</td>
              <td class="num" :class="{ unknown: !r.totals.costKnown }" :title="legacyNote(r.totals) || undefined">
                {{ spendUSD(r.totals) }}
              </td>
            </tr>
            <tr v-if="open[r.key]">
              <td colspan="5" class="calls">
                <div v-if="st(r.key).loading" class="faint">Loading…</div>
                <div v-else-if="st(r.key).error" class="banner danger">{{ st(r.key).error }}</div>
                <div v-else-if="!st(r.key).calls.length" class="faint">
                  No calls recorded for this run (cost only, from before token tracking).
                </div>
                <template v-else>
                  <ContextChart :calls="st(r.key).calls" />
                  <table class="list inner">
                    <thead>
                      <tr>
                        <th class="num">#</th>
                        <th>Time</th>
                        <th>Kind</th>
                        <th>Role</th>
                        <th>Engine / model</th>
                        <th>Resumed</th>
                        <th class="num" title="Uncommitted lines an implementer call left: its step commit">Lines</th>
                        <th class="num">In</th>
                        <th class="num">Out</th>
                        <th class="num">Cache read</th>
                        <th class="num">Cache write</th>
                        <th class="num">Duration</th>
                        <th class="num">Cost</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="c in st(r.key).calls" :key="c.key">
                        <td class="num">{{ c.key }}</td>
                        <td :title="fullTime(c.at)">{{ clockTime(c.at, now) }}</td>
                        <td>
                          {{ c.kind }}
                          <Badge
                            v-if="c.checkpoint"
                            tone="warning"
                            :title="`Its end saved the checkpoint commit ${shortSHA(c.checkpointSHA)}`"
                          >
                            💾 {{ c.checkpoint }}
                          </Badge>
                        </td>
                        <td>{{ c.role || '—' }}</td>
                        <td>
                          {{ c.engine }}<template v-if="c.model">/{{ c.model }}</template>
                          <span v-if="c.fallbackFrom" class="faint"> · fallback from {{ c.fallbackFrom }}</span>
                          <span v-if="c.readGateBlocks" class="faint" title="Reads the read gate blocked">
                            · {{ c.readGateBlocks }} gate {{ c.readGateBlocks === 1 ? 'block' : 'blocks' }}
                          </span>
                        </td>
                        <td>{{ c.resumed ? 'resumed' : '—' }}</td>
                        <td class="num">
                          {{ callLines(c) || '—' }}
                          <span
                            v-if="callNudges(c)"
                            class="faint"
                            title="How often the step-size gate told the implementer to close its step"
                          >
                            · {{ callNudges(c) }}
                          </span>
                        </td>
                        <td class="num">{{ tokenCount(c.totals.inputTokens) }}</td>
                        <td class="num">{{ tokenCount(c.totals.outputTokens) }}</td>
                        <td class="num">{{ tokenCount(c.totals.cacheReadTokens) }}</td>
                        <td class="num">{{ tokenCount(c.totals.cacheWriteTokens) }}</td>
                        <td class="num">{{ c.totals.durationMS ? duration(c.totals.durationMS) : '—' }}</td>
                        <td
                          class="num"
                          :class="{ unknown: c.costSource !== 'reported' }"
                          :title="c.costSource === 'estimated' ? 'Estimated from tokens and the pricing table' : costSourceLabel(c.costSource)"
                        >
                          {{ callCost(c) }}
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </section>
</template>

<style scoped>
.totals {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 18px;
  padding: 10px 16px;
  margin-bottom: 10px;
}
.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.unknown {
  color: var(--text-3);
}
.run {
  cursor: pointer;
}
.link {
  background: none;
  border: 0;
  padding: 0;
  color: var(--link, inherit);
  cursor: pointer;
  font: inherit;
}
.calls {
  padding: 8px 12px;
  background: var(--surface-2);
  overflow-x: auto;
}
</style>
