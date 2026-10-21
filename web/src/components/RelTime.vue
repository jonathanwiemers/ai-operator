<script setup lang="ts">
import { absTime, relTime } from '../format'
import { useNow } from '../live'

defineProps<{ time: string | undefined | null }>()
const now = useNow()
</script>

<template>
  <time v-if="time" :datetime="time" :title="absTime(time)">{{ relTime(time, now) }}</time>
  <span v-else class="faint">—</span>
</template>
