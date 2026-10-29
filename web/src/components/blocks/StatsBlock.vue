<script setup lang="ts">
// Key figures; a delta is coloured by its sign.
defineProps<{ stats: { label: string; value: string; delta?: string }[] }>()

function sign(delta: string): 'up' | 'down' | '' {
  const c = delta.trim()[0]
  return c === '+' || c === '↑' ? 'up' : c === '-' || c === '−' || c === '↓' ? 'down' : ''
}
</script>

<template>
  <div class="facts">
    <div v-for="(s, i) in stats" :key="i" class="fact">
      <div class="label">{{ s.label }}</div>
      <div class="value" :title="s.value">{{ s.value }}</div>
      <div v-if="s.delta" class="delta" :class="sign(s.delta)">{{ s.delta }}</div>
    </div>
  </div>
</template>

<style scoped>
.value {
  font-size: 20px;
}

.delta {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-3);
}

.delta.up {
  color: var(--tone-success-fg);
}

.delta.down {
  color: var(--tone-danger-fg);
}
</style>
