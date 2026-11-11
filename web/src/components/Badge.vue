<script setup lang="ts">
import UBadge from '@nuxt/ui/components/Badge.vue'
import { badgeLook } from '../badgeTone'
import type { Tone } from '../format'

withDefaults(defineProps<{ tone?: Tone; dot?: boolean; pulse?: boolean; title?: string }>(), {
  tone: 'neutral',
  dot: false,
  pulse: false,
  title: undefined,
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
      class="size-[7px] rounded-full bg-current"
      :class="{ 'animate-pulse motion-reduce:animate-none': pulse }"
    />
    <slot />
  </UBadge>
</template>
