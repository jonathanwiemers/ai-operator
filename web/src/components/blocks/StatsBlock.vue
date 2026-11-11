<script setup lang="ts">
// Key figures; a delta is coloured by its sign. The figures fill the row,
// at least 150 px each, so on a phone they stack two or one to a row. A long
// value wraps; nothing is cut short.
defineProps<{ stats: { label: string; value: string; delta?: string }[] }>()

function sign(delta: string): string {
  const c = delta.trim()[0]
  if (c === '+' || c === '↑') return 'text-(--tone-success-fg)'
  if (c === '-' || c === '−' || c === '↓') return 'text-(--tone-danger-fg)'
  return 'text-(--text-3)'
}
</script>

<template>
  <div class="facts" data-block="stats">
    <div v-for="(s, i) in stats" :key="i" class="fact">
      <div class="label">{{ s.label }}</div>
      <div class="text-xl font-[550] [overflow-wrap:anywhere]">{{ s.value }}</div>
      <div v-if="s.delta" class="text-xs font-semibold" :class="sign(s.delta)">{{ s.delta }}</div>
    </div>
  </div>
</template>
