<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { hasFeature } from '../api'
import { cost } from '../format'
import { queueDone } from '../milestoneQueue'
import { promptAnchor, splitPrompts } from '../promptCard'
import type { HistoryResponse, InboxItem, MilestoneView, QueueItem } from '../types'
import AskPilot from './AskPilot.vue'
import Badge from './Badge.vue'
import PauseCard from './PauseCard.vue'
import PromptCard from './PromptCard.vue'
import QueueList from './QueueList.vue'
import RelTime from './RelTime.vue'

// ContextPanel sits beside a project or milestone stream. On a milestone
// stream (full) it also holds the full pause card, the milestone's prompts
// (open first, the closed ones folded) and Ask the pilot; the stream keeps
// the compact pause card.
const props = defineProps<{
  namespace: string
  name: string
  inbox: InboxItem[]
  milestone?: MilestoneView | null
  history?: HistoryResponse | null
  full?: boolean
}>()

const queue = computed((): QueueItem[] => props.milestone?.autopilot?.items ?? [])
const done = computed(() => queueDone(queue.value))
const total = computed(() => queue.value.length)
const paused = computed(() => props.full && props.milestone?.autopilot?.phase === 'Paused')
const showPilotChat = computed(() => !!props.milestone?.permissions?.chat || hasFeature('chat'))
const prompts = computed(() => splitPrompts(props.milestone?.prompts))
// A Desk link to a closed prompt unfolds the closed ones.
const route = useRoute()
const closedLinked = computed(() => prompts.value.closed.some((p) => route.hash === `#${promptAnchor(p.id)}`))

const chart = computed(() => {
  const days = props.history?.days ?? []
  if (!days.length) return { bars: [], line: '' }
  const step = 300 / days.length
  const maxRuns = Math.max(1, ...days.map((d) => d.runs))
  const maxSpend = Math.max(1, ...days.map((d) => d.costUSD))
  const bars = days.map((d, i) => {
    const h = (d.runs / maxRuns) * 60
    return { x: i * step + 1, y: 64 - h, w: Math.max(step - 2, 1), h }
  })
  const line = days
    .map((d, i) => `${i * step + step / 2},${62 - (d.costUSD / maxSpend) * 58}`)
    .join(' ')
  return { bars, line }
})

function delta(cur: number, prev: number, suffix = ''): { text: string; cls: string } {
  const d = cur - prev
  if (d === 0) return { text: 'same', cls: '' }
  const sign = d > 0 ? '+' : ''
  return { text: `${sign}${typeof cur === 'number' && !Number.isInteger(d) ? d.toFixed(0) : d}${suffix} vs prev.`, cls: d > 0 ? 'up' : 'down' }
}

function spendDelta(cur: number, prev: number) {
  if (prev <= 0) return { text: cur > 0 ? 'new' : 'same', cls: '' }
  const pct = Math.round(((cur - prev) / prev) * 100)
  if (pct === 0) return { text: 'same', cls: '' }
  // Higher spend is "down" visually (worse).
  return { text: `${pct > 0 ? '+' : ''}${pct}%`, cls: pct > 0 ? 'down' : 'up' }
}

function inboxTo(item: InboxItem) {
  if (item.kind === 'task' && item.task) {
    return {
      name: 'task' as const,
      params: { namespace: item.namespace, name: item.project, task: item.task },
    }
  }
  if (item.milestoneIID) {
    return {
      name: 'stream-milestone' as const,
      params: { namespace: item.namespace, name: item.project, iid: String(item.milestoneIID) },
    }
  }
  return {
    name: 'stream-project' as const,
    params: { namespace: item.namespace, name: item.project },
  }
}
</script>

<template>
  <aside class="ctxp">
    <PauseCard
      v-if="paused && milestone"
      :key="milestone.iid"
      class="full-pause"
      :namespace="namespace"
      :name="name"
      :milestone="milestone"
    />

    <div class="panel">
      <h3>
        <span>Needs you</span>
        <span>{{ inbox.length }}</span>
      </h3>
      <div v-if="!inbox.length" class="empty-sm">Nothing waiting.</div>
      <div v-else class="need">
        <RouterLink v-for="(item, i) in inbox" :key="i" class="n-item" :to="inboxTo(item)">
          <b>{{ item.kind === 'proposal' && item.proposalID != null ? `P${item.proposalID}` : item.issueIID ? `#${item.issueIID}` : item.project }}</b>
          {{ item.title }}
          <small>{{ item.reason }}<template v-if="item.at"> · <RelTime :time="item.at" /></template></small>
        </RouterLink>
      </div>
    </div>

    <div v-if="milestone" class="panel">
      <h3>
        <span>{{ milestone.title }}</span>
        <span v-if="total">{{ done }} / {{ total }}</span>
      </h3>
      <QueueList :namespace="namespace" :name="name" :milestone="milestone" />
    </div>

    <div v-if="full && milestone && (milestone.pilot || prompts.open.length || prompts.closed.length)" class="panel">
      <h3>
        <span>Questions</span>
        <span v-if="prompts.open.length">{{ prompts.open.length }} open</span>
      </h3>
      <div v-if="milestone.pilot" class="row pilot">
        <span class="faint">Pilot</span>
        <Badge :tone="milestone.pilot.autonomy === 'managed' ? 'progress' : 'neutral'">{{ milestone.pilot.autonomy }}</Badge>
        <span class="faint">{{ cost(milestone.pilot.costUSD) }}</span>
        <span class="spacer" />
        <RouterLink :to="{ name: 'task', params: { namespace, name, task: milestone.pilot.task } }">Pilot task</RouterLink>
      </div>
      <div class="prompts">
        <div v-if="!prompts.open.length" class="empty-sm">No open questions.</div>
        <PromptCard v-for="p in prompts.open" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
        <details v-if="prompts.closed.length" class="earlier" :open="closedLinked">
          <summary>Earlier questions ({{ prompts.closed.length }})</summary>
          <div class="prompts">
            <PromptCard v-for="p in prompts.closed" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
          </div>
        </details>
      </div>
    </div>

    <div v-if="history" class="panel">
      <h3><span>Last 30 days</span></h3>
      <svg class="chart" viewBox="0 0 300 64" preserveAspectRatio="none" aria-label="Runs per day and spend">
        <rect v-for="(b, i) in chart.bars" :key="i" :x="b.x" :y="b.y" :width="b.w" :height="b.h" rx="1.5" fill="var(--tone-progress-fg)" opacity=".55" />
        <polyline v-if="chart.line" fill="none" stroke="var(--accent-2)" stroke-width="2" vector-effect="non-scaling-stroke" :points="chart.line" />
      </svg>
      <div class="legend">
        <span><i class="runs" />runs / day</span>
        <span><i class="spend" />spend</span>
      </div>
      <div class="stats">
        <div class="stat">
          <div class="v">{{ history.totals.runs }}</div>
          <div class="l">runs</div>
          <div class="d" :class="delta(history.totals.runs, history.previous.runs).cls">
            {{ delta(history.totals.runs, history.previous.runs).text }}
          </div>
        </div>
        <div class="stat">
          <div class="v">{{ history.totals.merged }}</div>
          <div class="l">MRs merged</div>
          <div class="d" :class="delta(history.totals.merged, history.previous.merged).cls">
            {{ delta(history.totals.merged, history.previous.merged).text }}
          </div>
        </div>
        <div class="stat">
          <div class="v">{{ cost(String(history.totals.costUSD)) }}</div>
          <div class="l">model spend</div>
          <div class="d" :class="spendDelta(history.totals.costUSD, history.previous.costUSD).cls">
            {{ spendDelta(history.totals.costUSD, history.previous.costUSD).text }}
          </div>
        </div>
        <div class="stat">
          <div class="v">{{ history.totals.events }}</div>
          <div class="l">events</div>
          <div class="d" :class="delta(history.totals.events, history.previous.events).cls">
            {{ delta(history.totals.events, history.previous.events).text }}
          </div>
        </div>
      </div>
    </div>

    <div v-if="full && milestone && showPilotChat" class="panel">
      <h3><span>Ask the pilot</span></h3>
      <AskPilot :namespace="namespace" :name="name" :milestone="milestone" />
    </div>
  </aside>
</template>

<style scoped>
.ctxp {
  border-left: 1px solid var(--border);
  background: var(--surface);
  overflow-y: auto;
  padding: 16px;
  display: grid;
  gap: 14px;
  align-content: start;
  min-height: 0;
}
/* The full pause card sits on top, without the page's spacing. */
.ctxp .full-pause {
  margin-top: 0;
}
.panel h3 {
  font-size: 11.5px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-3);
  margin: 0 0 8px;
  display: flex;
  justify-content: space-between;
  font-weight: 600;
}
.panel {
  border-bottom: 1px solid var(--border);
  padding-bottom: 14px;
}
.panel:last-child {
  border-bottom: 0;
  padding-bottom: 0;
}
.need {
  display: grid;
  gap: 8px;
}
.need a.n-item {
  display: block;
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--tone-warning-bg);
  color: var(--text);
  font-size: 13px;
}
.need a.n-item:hover {
  text-decoration: none;
  filter: brightness(0.98);
}
.need a.n-item small {
  display: block;
  color: var(--text-2);
  font-size: 12px;
}
.stats {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin-top: 10px;
}
.stat {
  background: var(--surface-2);
  border-radius: 8px;
  padding: 8px 10px;
}
.stat .v {
  font-size: 17px;
  font-weight: 700;
  letter-spacing: -0.01em;
}
.stat .l {
  font-size: 11.5px;
  color: var(--text-3);
}
.stat .d {
  font-size: 11.5px;
  font-weight: 600;
}
.up {
  color: var(--tone-success-fg);
}
.down {
  color: var(--tone-danger-fg);
}
.chart {
  width: 100%;
  height: 64px;
  display: block;
}
.legend {
  display: flex;
  gap: 12px;
  font-size: 11.5px;
  color: var(--text-3);
  margin-top: 4px;
}
.legend i {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 2px;
  margin-right: 4px;
  vertical-align: -1px;
}
.legend i.runs {
  background: var(--tone-progress-fg);
}
.legend i.spend {
  background: var(--accent-2);
}
.pilot {
  font-size: 13px;
  margin-bottom: 10px;
}
.prompts {
  display: grid;
  gap: 10px;
}
.earlier summary {
  cursor: pointer;
  font-size: 13px;
  color: var(--text-2);
}
.earlier[open] summary {
  margin-bottom: 10px;
}
.empty-sm {
  font-size: 13px;
  color: var(--text-3);
}
</style>
