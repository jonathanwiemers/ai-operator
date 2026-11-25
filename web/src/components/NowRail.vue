<script setup lang="ts">
import UIcon from '@nuxt/ui/components/Icon.vue'
import { computed } from 'vue'
import type { NowCounts } from '../nowAndNext'

// NowRail is the collapsed Now & next sidebar's counts (R8 of "Desk: the
// Cockpit newest first by issue"), in ContextLayout's rail slot under the
// expand button: running, waiting and next, each an icon over its number.
// The number and icon are hidden from screen readers, which read the label
// ("2 running") instead.
const props = defineProps<{ counts: NowCounts }>()
const rows = computed(() => [
  { id: 'running', icon: 'i-lucide-activity', n: props.counts.running, label: `${props.counts.running} running` },
  { id: 'waiting', icon: 'i-lucide-history', n: props.counts.waiting, label: `${props.counts.waiting} waiting` },
  { id: 'next', icon: 'i-lucide-arrow-right', n: props.counts.next, label: `${props.counts.next} next` },
])
</script>

<template>
  <ul class="rail" aria-label="Now & next counts" data-now-rail>
    <li v-for="r in rows" :key="r.id" :class="{ zero: r.n === 0 }" :data-now-count="r.id">
      <UIcon :name="r.icon" class="size-4" aria-hidden="true" />
      <span aria-hidden="true">{{ r.n }}</span>
      <span class="sr-only">{{ r.label }}</span>
    </li>
  </ul>
</template>

<!-- Scoped, not Tailwind utilities: they load with the page, not with the
     initial stylesheet (docs/contributing/web-ui.md, "Bundle budget"). -->
<style scoped>
.rail {
  display: grid;
  gap: 12px;
  margin: 0;
  padding: 0;
  list-style: none;
}
li {
  display: grid;
  justify-items: center;
  gap: 2px;
  font-size: 12px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
}
li.zero {
  color: var(--text-3);
}
</style>
