<script setup lang="ts">
// The phone's bottom tab bar (docs/contributing/web-ui.md, "Navigation" and
// "UI kit"): Nuxt UI has no tab bar. Inbox with its count, Overview, Projects
// (opens the rail sheet), Metrics and Research with their features on; the
// section of the current page has aria-current. App.vue shows it below 760 px.
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import UButton from '@nuxt/ui/components/Button.vue'
import UIcon from '@nuxt/ui/components/Icon.vue'
import { hasFeature, isResearchPath } from '../api'

defineProps<{
  inboxCount: number
  // The rail sheet is open: aria-expanded on Projects.
  menuOpen: boolean
}>()
// menu: Projects was tapped, App.vue opens the rail sheet.
const emit = defineEmits<{ menu: [] }>()

const route = useRoute()
const section = computed(() => {
  if (route.name === 'inbox') return 'inbox'
  if (route.name === 'overview') return 'overview'
  if (route.name === 'metrics') return 'metrics'
  if (isResearchPath(route.path)) return 'research'
  return null
})
const current = (s: string) => (section.value === s ? 'page' : undefined)

// Each item fills its share of the bar, the whole height a hit area; the
// bar's top border takes the last pixel of --bottombar (set by App.vue).
const tab =
  'flex h-[calc(var(--bottombar)-1px)] min-w-0 flex-1 flex-col items-center justify-center gap-0.5 text-[11px] font-semibold text-(--text-2) hover:no-underline aria-[current=page]:text-(--accent)'
</script>

<template>
  <nav
    class="bottombar fixed inset-x-0 bottom-0 z-10 flex border-t border-(--border) bg-(--surface) pe-[env(safe-area-inset-right)] ps-[env(safe-area-inset-left)] pb-[env(safe-area-inset-bottom)] tablet:hidden"
    aria-label="Sections"
  >
    <RouterLink to="/inbox" :class="tab" :aria-current="current('inbox')" :aria-label="inboxCount ? `Inbox (${inboxCount})` : undefined">
      <span class="relative flex">
        <UIcon name="i-lucide-inbox" class="size-5" />
        <!-- Counts take the danger tone, never the primary colour of the buttons. -->
        <span
          v-if="inboxCount"
          class="absolute -top-1.5 left-3 min-w-[18px] rounded-[9px] bg-(--tone-danger-bg) px-1.5 py-px text-center text-[11px] font-bold text-(--tone-danger-fg)"
          >{{ inboxCount }}</span
        >
      </span>
      Inbox
    </RouterLink>
    <RouterLink to="/" :class="tab" :aria-current="current('overview')">
      <UIcon name="i-lucide-layout-dashboard" class="size-5" />
      Overview
    </RouterLink>
    <UButton
      color="neutral"
      variant="ghost"
      icon="i-lucide-folders"
      label="Projects"
      aria-haspopup="dialog"
      :aria-expanded="menuOpen"
      :class="[tab, 'rounded-none p-0']"
      :ui="{ leadingIcon: 'size-5', label: 'max-w-full' }"
      @click="emit('menu')"
    />
    <RouterLink v-if="hasFeature('history')" to="/metrics" :class="tab" :aria-current="current('metrics')">
      <UIcon name="i-lucide-receipt" class="size-5" />
      Metrics
    </RouterLink>
    <RouterLink v-if="hasFeature('research')" to="/research" :class="tab" :aria-current="current('research')">
      <UIcon name="i-lucide-telescope" class="size-5" />
      Research
    </RouterLink>
  </nav>
</template>
