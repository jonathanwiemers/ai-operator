<script setup lang="ts">
import Badge from './Badge.vue'
import { stageInfo } from '../format'
import type { Stage } from '../types'

defineProps<{ stage: Stage }>()
</script>

<template>
  <Badge
    :tone="stageInfo[stage].tone"
    :title="stageInfo[stage].hint"
    dot
    :pulse="stage === 'working'"
  >
    {{ stageInfo[stage].label }}
  </Badge>
</template>
