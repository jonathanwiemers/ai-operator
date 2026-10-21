<script setup lang="ts">
import type { Tone } from '../format'

withDefaults(defineProps<{ tone?: Tone; dot?: boolean; pulse?: boolean; title?: string }>(), {
  tone: 'neutral',
  dot: false,
  pulse: false,
  title: undefined,
})
</script>

<template>
  <span class="badge" :class="'tone-' + tone" :title="title">
    <span v-if="dot" class="dot" :class="{ pulse }" />
    <slot />
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 22px;
  padding: 0 8px;
  border-radius: 999px;
  font-size: 12px;
  font-weight: 600;
  white-space: nowrap;
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: currentColor;
}

.pulse {
  animation: pulse 1.6s ease-in-out infinite;
}

@keyframes pulse {
  50% {
    opacity: 0.3;
  }
}

@media (prefers-reduced-motion: reduce) {
  .pulse {
    animation: none;
  }
}

.tone-neutral {
  background: var(--tone-neutral-bg);
  color: var(--tone-neutral-fg);
}
.tone-muted {
  background: var(--tone-muted-bg);
  color: var(--tone-muted-fg);
}
.tone-info {
  background: var(--tone-info-bg);
  color: var(--tone-info-fg);
}
.tone-progress {
  background: var(--tone-progress-bg);
  color: var(--tone-progress-fg);
}
.tone-success {
  background: var(--tone-success-bg);
  color: var(--tone-success-fg);
}
.tone-warning {
  background: var(--tone-warning-bg);
  color: var(--tone-warning-fg);
}
.tone-danger {
  background: var(--tone-danger-bg);
  color: var(--tone-danger-fg);
}
</style>
