<script setup lang="ts">
import UProgress from '@nuxt/ui/components/Progress.vue'
import type { GitLabUsage } from '../gitlabLimit'

// GitLabUsage is the project header's GitLab line (R12 of
// docs/specs/gitlab-rate-limit.spec.md, gitlabHeader): the bot token's
// requests this minute as a bar, in the warning colour at or below the
// reserve, and while the project's runs wait for the limit, until when.
defineProps<{ usage: GitLabUsage | null; limited: string | null }>()
</script>

<template>
  <span class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-(--text-2)" data-gitlab-usage>
    <span
      v-if="usage"
      class="flex items-center gap-2 whitespace-nowrap tabular-nums"
      :class="usage.low && 'text-(--tone-warning-fg)'"
      :data-low="usage.low || undefined"
      data-gitlab-usage-bar
    >
      <UProgress
        class="w-24"
        size="xs"
        :model-value="Math.min(usage.observed, usage.limit)"
        :max="usage.limit"
        :color="usage.low ? 'warning' : 'info'"
        :get-value-label="() => usage!.label"
      />
      {{ usage.label }}
    </span>
    <span v-if="limited" class="font-semibold whitespace-nowrap text-(--tone-warning-fg)" data-gitlab-limited>{{ limited }}</span>
  </span>
</template>
