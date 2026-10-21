<script setup lang="ts">
import { computed } from 'vue'
import { safeURL } from '../format'

// A link that leaves the UI (GitLab). Unsafe URLs render as plain text.
const props = defineProps<{ href: string | undefined | null }>()
const url = computed(() => safeURL(props.href))
</script>

<template>
  <a v-if="url" :href="url" target="_blank" rel="noopener noreferrer" class="ext">
    <slot />
    <svg class="icon" viewBox="0 0 12 12" aria-hidden="true">
      <path
        d="M4.5 2.5h-2v7h7v-2M7 2.5h2.5V5M9.5 2.5 5.5 6.5"
        fill="none"
        stroke="currentColor"
        stroke-width="1.3"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  </a>
  <span v-else><slot /></span>
</template>

<style scoped>
.ext {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

.icon {
  width: 11px;
  height: 11px;
  opacity: 0.7;
  flex: none;
}
</style>
