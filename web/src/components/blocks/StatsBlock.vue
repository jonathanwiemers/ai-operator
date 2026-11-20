<script setup lang="ts">
import StatTile from '../StatTile.vue'

// Key figures as stat tiles; a delta is coloured by its sign. The tiles fill
// the row, at least 9rem each, so on a phone they stack two or one to a row.
// A long value wraps; nothing is cut short.
defineProps<{ stats: { label: string; value: string; delta?: string }[] }>()

function tone(delta: string | undefined): 'up' | 'down' | 'neutral' {
  const c = delta?.trim()[0]
  if (c === '+' || c === '↑') return 'up'
  if (c === '-' || c === '−' || c === '↓') return 'down'
  return 'neutral'
}
</script>

<template>
  <div class="stat-tiles" data-block="stats">
    <StatTile v-for="(s, i) in stats" :key="i" :label="s.label" :value="s.value" :delta="s.delta" :tone="tone(s.delta)" />
  </div>
</template>
