<script setup lang="ts">
// A research schedule in words ("daily at 07:15 Europe/Berlin", cronText)
// with the raw cron and time zone as tooltip. A form cronText does not read
// shows raw, as code with its time zone.
import { computed } from 'vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import { cronRaw, cronText } from '../cron'

const props = defineProps<{ schedule: string; timeZone?: string }>()
const words = computed(() => cronText(props.schedule, props.timeZone))
</script>

<template>
  <UTooltip v-if="words" :text="cronRaw(schedule, timeZone)">
    <span class="[overflow-wrap:anywhere]" data-schedule>{{ words }}</span>
  </UTooltip>
  <span v-else class="[overflow-wrap:anywhere]" data-schedule><code>{{ schedule }}</code> {{ timeZone || 'UTC' }}</span>
</template>
