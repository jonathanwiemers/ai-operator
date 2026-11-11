<script setup lang="ts">
// A section heading of a result; level 3 is a subsection.
defineProps<{ text: string; level?: number }>()
</script>

<template>
  <h3 v-if="level === 3" class="mt-2 text-[15px] tracking-normal text-(--text) normal-case [overflow-wrap:anywhere]">{{ text }}</h3>
  <h2 v-else class="mt-2 text-lg [overflow-wrap:anywhere]">{{ text }}</h2>
</template>
