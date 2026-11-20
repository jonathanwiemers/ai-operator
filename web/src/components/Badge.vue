<script setup lang="ts">
import UBadge from '@nuxt/ui/components/Badge.vue'
import { badgeLook } from '../badgeTone'
import type { Tone } from '../format'

// `label` names the state of a dot-only badge for a screen reader (Decision 8
// of docs/specs/desk-accessible.spec.md: no status by colour alone). With an
// empty slot it is the badge's text: visually hidden next to a dot, visible
// without one. Slot text, when there is some, names the state itself.
withDefaults(defineProps<{ tone?: Tone; dot?: boolean; pulse?: boolean; title?: string; label?: string }>(), {
  tone: 'neutral',
  dot: false,
  pulse: false,
  title: undefined,
  label: undefined,
})
</script>

<template>
  <UBadge
    :color="badgeLook[tone].color"
    :variant="badgeLook[tone].variant"
    :class="['h-[22px] gap-1.5 rounded-full px-2 text-xs font-semibold whitespace-nowrap', badgeLook[tone].class]"
    :title="title"
  >
    <span
      v-if="dot"
      aria-hidden="true"
      class="size-[7px] rounded-full bg-current"
      :class="{ 'animate-pulse motion-reduce:animate-none': pulse }"
    />
    <slot>
      <span v-if="label" :class="{ 'sr-only': dot }">{{ label }}</span>
    </slot>
  </UBadge>
</template>
