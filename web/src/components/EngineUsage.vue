<script setup lang="ts">
import { absTime, clockTime, windowLabel } from '../format'
import { useNow } from '../live'
import type { EngineUsage, UsageWindow } from '../types'

// One line per engine reading, e.g. "claude: 5 h 2 % (resets 14:00),
// 7 days 16 % (resets Mon 09:00), as of 12:31". A window whose reset has
// passed was reset since the reading: it is stale.
defineProps<{ usage: EngineUsage }>()
const now = useNow()

function stale(w: UsageWindow) {
  return !!w.resetsAt && Date.parse(w.resetsAt) <= now.value
}

function reset(w: UsageWindow) {
  if (!w.resetsAt) return ''
  return ` (${stale(w) ? 'stale, reset' : 'resets'} ${clockTime(w.resetsAt, now.value)})`
}
</script>

<template>
  <div class="usage">
    <span class="engine">{{ usage.engine }}:</span>
    <span
      v-for="(w, i) in usage.windows"
      :key="w.name"
      class="window"
      :class="{ stale: stale(w), high: !stale(w) && w.percent >= 80 }"
      :title="w.resetsAt ? `resets ${absTime(w.resetsAt)}` : undefined"
    >
      {{ windowLabel(w.name) }} <strong>{{ w.percent }} %</strong>{{ reset(w) }}{{ i < (usage.windows?.length ?? 0) - 1 ? ',' : '' }}
    </span>
    <span v-if="usage.status && usage.status !== 'allowed'" class="window high">{{ usage.status.replaceAll('_', ' ') }}</span>
    <span class="faint" :title="absTime(usage.observedAt)">as of {{ clockTime(usage.observedAt, now) }}</span>
  </div>
</template>

<style scoped>
.usage {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 6px;
  font-size: 13px;
  color: var(--text-2);
}

.engine {
  font-weight: 600;
  color: var(--text);
}

.window strong {
  font-weight: 600;
  color: var(--text);
}

.high strong,
.window.high {
  color: var(--tone-warning-fg);
}

.stale,
.stale strong {
  color: var(--text-3);
}
</style>
