<script setup lang="ts">
import UCard from '@nuxt/ui/components/Card.vue'
import { computed } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

// One stat tile (decision 7), the one look of key figures on every page: a
// label, the value, and optionally a delta coloured by its tone and a sub
// line, on a compact UCard. With `to` the whole tile is the link. warn
// colours a value that asks for someone (paused, needs you). Rows of tiles
// sit in a `stat-tiles` grid (style.css). The value and sub slots take
// markup beyond text (a tooltip, an italic estimate).
const props = withDefaults(
  defineProps<{
    label: string
    value?: string | number
    delta?: string
    tone?: 'up' | 'down' | 'neutral'
    sub?: string
    to?: RouteLocationRaw
    warn?: boolean
  }>(),
  { value: undefined, delta: undefined, tone: 'neutral', sub: undefined, to: undefined },
)

const toneClass = { up: 'text-(--tone-success-fg)', down: 'text-(--tone-danger-fg)', neutral: 'text-(--text-3)' }
const ui = computed(() => ({
  root: ['min-w-0', props.to ? 'hover:bg-(--surface-2) hover:no-underline' : ''].join(' '),
  body: 'px-4 py-3',
}))
</script>

<template>
  <UCard :as="to ? RouterLink : 'div'" :to="to" :ui="ui" data-stat-tile>
    <dl class="m-0 flex min-w-0 flex-col gap-1">
      <dt class="text-[11.5px] text-(--text-3)">{{ label }}</dt>
      <dd
        class="m-0 text-xl font-semibold tabular-nums [overflow-wrap:anywhere]"
        :class="warn ? 'text-(--tone-warning-fg)' : 'text-(--text)'"
      >
        <slot>{{ value }}</slot>
      </dd>
      <dd v-if="delta" class="m-0 text-xs font-semibold" :class="toneClass[tone]">{{ delta }}</dd>
      <dd v-if="sub || $slots.sub" class="m-0 text-[13px] text-(--text-2) [overflow-wrap:anywhere]">
        <slot name="sub">{{ sub }}</slot>
      </dd>
    </dl>
  </UCard>
</template>
