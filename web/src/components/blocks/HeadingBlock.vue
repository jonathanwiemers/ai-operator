<script setup lang="ts">
// A section heading of a result; level 3 is a subsection.
defineProps<{ text: string; level?: number }>()
</script>

<template>
  <h3 v-if="level === 3" class="heading sub">{{ text }}</h3>
  <h2 v-else class="heading">{{ text }}</h2>
</template>

<style scoped>
.heading {
  margin-top: 8px;
  font-size: 18px;
}

.heading.sub {
  font-size: 15px;
  text-transform: none;
  letter-spacing: normal;
  color: var(--text);
}
</style>
