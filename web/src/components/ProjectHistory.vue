<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UCollapsible from '@nuxt/ui/components/Collapsible.vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import { computed, ref } from 'vue'
import { cost } from '../format'
import { dayEndCursor, heatCellLabel, heatLevel, heatSummary } from '../stream'
import type { HistoryResponse, MilestoneView } from '../types'
import CompanionsList from './CompanionsList.vue'

// ProjectHistory is the History section at the bottom of the project's Now
// & next sidebar (R9 of "Desk: the Cockpit newest first by issue"), once the
// project panel's Queue view: the active milestone's companions and the last
// 30 days. Closed by default and not remembered (D822-P2); open while a day
// is picked, so a reopened slide-over shows it. A day of the history pages
// the project's Cockpit back to that day (emit day; the picked one is
// jumped, a dayEndCursor; clear goes back to now).
// Refs, URLs and titles wrap anywhere, so the sidebar never scrolls sideways.
const props = defineProps<{
  namespace: string
  name: string
  milestone?: MilestoneView | null
  history?: HistoryResponse | null
  jumped?: string
}>()
const emit = defineEmits<{ day: [day: string]; clear: [] }>()
const open = ref(!!props.jumped)

// The history's day cells: one per day, shaded by its events, a button
// that pages the Cockpit back to the end of that day. One tooltip for all
// of them (no tooltip per cell), anchored to the cell under the pointer or
// with focus.
const heatDays = computed(() => {
  const days = props.history?.days ?? []
  const max = Math.max(1, ...days.map((d) => d.events))
  return days.map((d) => ({ ...d, level: heatLevel(d.events, max), label: heatCellLabel(d.day, d.events) }))
})
const heatDescription = computed(() => heatSummary(heatDays.value))
const heatHint = ref<{ el: HTMLElement; text: string } | null>(null)
function showHeatHint(e: Event) {
  const el = (e.target as HTMLElement | null)?.closest<HTMLElement>('[data-heat-day]')
  heatHint.value = el ? { el, text: el.getAttribute('aria-label') ?? '' } : null
}
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

</script>

<template>
  <UCollapsible v-model:open="open" class="history" data-project-history>
    <template #default="{ open: shown }">
      <UButton
        color="neutral"
        variant="ghost"
        size="sm"
        block
        label="History"
        icon="i-lucide-history"
        :trailing-icon="shown ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
        :ui="{ trailingIcon: 'ms-auto' }"
        class="min-h-11 tablet:min-h-0"
        data-project-history-toggle
      />
    </template>
    <template #content>
      <div class="ctxp">
        <div v-if="milestone && milestone.companions?.length" class="panel">
          <h3>
            <span>Companions</span>
            <span>{{ milestone.companions.length }}</span>
          </h3>
          <CompanionsList :namespace="namespace" :name="name" :milestone="milestone" />
        </div>

        <div v-if="history" class="panel">
          <h3>
            <span>Last 30 days</span>
            <UButton v-if="jumped" color="neutral" variant="link" size="xs" class="p-0 text-[11.5px] text-(--link)" label="Back to now" data-heat-clear @click="emit('clear')" />
          </h3>
          <p v-if="heatDays.length" class="sr-only" data-heat-summary>{{ heatDescription }}</p>
          <div v-if="heatDays.length" class="cells" title="Events per day; pick one to page the Cockpit back to it" @pointerover="showHeatHint" @focusin="showHeatHint" @pointerleave="heatHint = null" @focusout="heatHint = null">
            <!-- A cell's look is the heatmap's (.cells button below), not a button's. -->
            <UButton
              v-for="d in heatDays"
              :key="d.day"
              color="neutral"
              variant="ghost"
              :data-l="d.level"
              :data-heat-day="d.day"
              :aria-label="d.label"
              :aria-pressed="jumped === dayEndCursor(d.day)"
              :class="{ sel: jumped === dayEndCursor(d.day) }"
              @click="emit('day', d.day)"
            />
          </div>
          <UTooltip :open="!!heatHint" :reference="heatHint?.el" :text="heatHint?.text" />
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
      </div>
    </template>
  </UCollapsible>
</template>

<style scoped>
.history {
  min-width: 0;
  border-top: 1px solid var(--border);
  padding-top: 8px;
}
.ctxp {
  padding: 8px 0 0;
  display: grid;
  /* A row's min-content (a nowrap companion reason) never widens the panel
     past its own width, in the 320 px column or the sheet. */
  grid-template-columns: minmax(0, 1fr);
  gap: 14px;
  align-content: start;
  min-height: 0;
}
.panel h3 {
  gap: 8px;
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
  min-width: 0;
  border-bottom: 1px solid var(--border);
  padding-bottom: 14px;
}
.panel:last-child {
  border-bottom: 0;
  padding-bottom: 0;
}
/* The days wrap in rows of cells at least 24 px wide and high, the target
   size of WCAG 2.2 (2.5.8): about ten to a row in the 320 px column. */
.cells {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(24px, 1fr));
  gap: 3px;
  margin-bottom: 10px;
}
.cells button {
  min-width: 24px;
  height: 24px;
  border-radius: 3px;
  background: var(--surface-3);
  border: 0;
  cursor: pointer;
  padding: 0;
}
.cells button[data-l='1'] {
  background: color-mix(in srgb, var(--tone-progress-fg) 25%, var(--surface-3));
}
.cells button[data-l='2'] {
  background: color-mix(in srgb, var(--tone-progress-fg) 50%, var(--surface-3));
}
.cells button[data-l='3'] {
  background: color-mix(in srgb, var(--tone-progress-fg) 80%, var(--surface-3));
}
.cells button.sel {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
/* On phones every button is at least 44 px wide and high (style.css, "Hit
   areas"): the columns grow to match, so no cell reaches past the panel. */
@media (max-width: 759.98px) {
  .cells {
    grid-template-columns: repeat(auto-fill, minmax(44px, 1fr));
  }
  .cells button {
    height: 44px;
  }
}
.stats {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin-top: 10px;
}
.stat {
  min-width: 0;
  overflow-wrap: anywhere;
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
  flex-wrap: wrap;
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
</style>
