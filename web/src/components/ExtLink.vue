<script setup lang="ts">
import { computed } from 'vue'
import { safeURL } from '../format'

// A link that leaves the UI (GitLab, or a source of a research result:
// nofollow). Unsafe URLs render as plain text. `ext` is a hook for
// Markdown.vue's doc variant, which sets links inline.
const props = defineProps<{ href: string | undefined | null; nofollow?: boolean }>()
const url = computed(() => safeURL(props.href))
</script>

<template>
  <a v-if="url" :href="url" target="_blank" :rel="nofollow ? 'noopener noreferrer nofollow' : 'noopener noreferrer'" class="ext inline-flex items-center gap-[3px]">
    <slot />
    <svg class="size-[11px] flex-none opacity-70" viewBox="0 0 12 12" aria-hidden="true">
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
