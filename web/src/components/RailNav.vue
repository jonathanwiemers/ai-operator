<script setup lang="ts">
// The rail's content (docs/contributing/web-ui.md, "Navigation"): a vertical
// UNavigationMenu with Inbox, Overview, Metrics, Plans, the projects with
// their sets and the research (src/railItems.ts), then a footer with the
// theme switch, the engine meters and the user. App.vue renders it in the
// sidebar on tablet and desktop and in the rail sheet on phones; App.vue
// loads the data. Collapsed (the sidebar at 64 px) it shows only the pages,
// as icons with their names as tooltips, and no footer.
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { hasFeature, session } from '../api'
import { logout } from '../client'
import { aboutLine } from '../about'
import { loginLasts, windowLabel } from '../format'
import { live } from '../live'
import { openSets, railItems } from '../railItems'
import type { EngineUsage, ProjectSummary, ResearchSummary } from '../types'
import type { ProjectSetSummary } from '../generated/views'
import UButton from '@nuxt/ui/components/Button.vue'
import UNavigationMenu from '@nuxt/ui/components/NavigationMenu.vue'
import UProgress from '@nuxt/ui/components/Progress.vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import Truncated from '../ui/Truncated.vue'
import RelTime from './RelTime.vue'
import ThemeSwitch from './ThemeSwitch.vue'

const props = defineProps<{
  projects: ProjectSummary[] | null
  // null when not loaded or the request failed: the flat list as before.
  projectSets?: ProjectSetSummary[] | null
  researches: ResearchSummary[] | null
  inboxCount: number
  // The sidebar is collapsed (App.vue): the pages as icons, the rest hidden.
  collapsed?: boolean
}>()

// navigate: a link was selected. The sheet closes on it, also for a link to
// the page it is on, which the router does not navigate to again.
const emit = defineEmits<{ navigate: [] }>()

const route = useRoute()
const about = computed(() => (session.about ? aboutLine(session.about) : null))
const lasts = computed(() => (session.me?.authMode === 'oidc' ? loginLasts(session.me, Date.now()) : null))

const items = computed(() =>
  railItems({
    path: route.path,
    inboxCount: props.inboxCount,
    projects: props.projects,
    projectSets: props.projectSets,
    researches: props.researches,
    features: { history: hasFeature('history'), plans: hasFeature('plans'), research: hasFeature('research') },
    collapsed: props.collapsed,
    onSelect: () => emit('navigate'),
  }),
)

// The open sets. The projects load after the menu mounts, so defaultOpen
// alone would not open a set: a set the route enters is added here, and one
// the owner opened stays open.
const open = ref<string[]>([])
watch(
  () => openSets(items.value),
  (want) => {
    const missing = want.filter((v) => !open.value.includes(v))
    if (missing.length) open.value = [...open.value, ...missing]
  },
  { immediate: true },
)
// Only the accordion's lists count: the menu's root also emits a string
// ('' when a link click dismisses it), which must not close the sets.
function onOpen(v: string | string[] | undefined) {
  if (Array.isArray(v)) open.value = v
}

const engineMeters = computed(() => {
  const byEngine = new Map<string, EngineUsage>()
  for (const p of props.projects ?? []) {
    for (const u of p.engineUsage ?? []) {
      const prev = byEngine.get(u.engine)
      if (!prev || Date.parse(u.observedAt) > Date.parse(prev.observedAt)) byEngine.set(u.engine, u)
    }
  }
  return [...byEngine.values()]
})
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <!-- Links are controls: 44 px on phones, like the buttons (style.css,
         "Hit areas"). -->
    <UNavigationMenu
      aria-label="Main"
      :model-value="open"
      type="multiple"
      orientation="vertical"
      color="neutral"
      :collapsed="collapsed"
      tooltip
      :items="items"
      :class="['min-h-0 flex-1 gap-0 overflow-y-auto pb-3', collapsed ? 'px-2 pt-1' : 'px-2']"
      :ui="{
        separator: 'hidden',
        label: 'px-2.5 pt-3.5 pb-1.5 text-[11px] uppercase tracking-[0.06em] text-(--text-3)',
        link: ['max-tablet:min-h-11', collapsed && 'justify-center'],
        linkLabel: 'min-w-0 flex-1',
        childList: 'ms-4',
      }"
      @update:model-value="onOpen"
    >
      <!-- The label with its sub-line (state, schedule), which wraps. -->
      <template #item-label="{ item }">
        <span class="block truncate">{{ item.label }}</span>
        <UTooltip v-if="item.description" :text="item.hint" :disabled="!item.hint">
          <span class="block truncate text-[11.5px] font-normal text-(--text-3)">{{ item.description }}</span>
        </UTooltip>
      </template>
      <!-- A set's header: name, counts and last discovery; the warning tone
           with the reason as tooltip while Ready or Discovered is False. -->
      <template #set-label="{ item }">
        <UTooltip :text="item.problem ?? undefined" :disabled="!item.problem">
          <span data-rail-set class="block" :class="item.problem ? 'text-(--tone-warning-fg)' : undefined">
            <span class="block truncate">{{ item.label }}</span>
            <span class="block text-[11.5px] font-normal whitespace-normal" :class="item.problem ? undefined : 'text-(--text-3)'">
              <span data-set-counts>{{ item.description }}</span>
              <span v-if="item.discoveredAt"> · discovered <RelTime :time="item.discoveredAt" /></span>
              <span v-else> · not discovered yet</span>
            </span>
            <span v-if="item.problem" class="sr-only">{{ item.problem }}</span>
          </span>
        </UTooltip>
      </template>
    </UNavigationMenu>

    <div v-if="!collapsed" class="rail-foot grid gap-1.5 border-t border-(--border) px-3.5 py-2.5 text-xs text-(--text-2)">
      <ThemeSwitch />
      <div v-for="u in engineMeters" :key="u.engine" class="grid gap-1.5">
        <div v-for="w in u.windows ?? []" :key="w.name" class="grid grid-cols-[72px_1fr_34px] items-center gap-2 text-[11.5px] whitespace-nowrap">
          <span>{{ u.engine }} {{ windowLabel(w.name) }}</span>
          <UProgress :model-value="Math.min(100, w.percent)" size="xs" :color="w.percent >= 80 ? 'warning' : 'info'" :aria-label="`${u.engine} ${windowLabel(w.name)}`" />
          <span>{{ w.percent }}%</span>
        </div>
      </div>
      <div class="who mt-1 flex flex-wrap items-center gap-2">
        <UTooltip :text="live.connected ? 'Updates arrive live' : 'Reconnecting…'">
          <span class="live inline-flex items-center gap-1.5 text-xs font-semibold" :class="live.connected ? 'text-(--tone-success-fg)' : 'text-(--text-3)'">
            <span class="size-2 rounded-full" :class="live.connected ? 'bg-(--tone-success-fg) ring-3 ring-(--tone-success-bg)' : 'bg-(--text-3)'" />{{
              live.connected ? 'Live' : 'Offline'
            }}
          </span>
        </UTooltip>
        <!-- A clipped name opens on tap; the roles stay a hover hint, nothing depends on them. -->
        <Truncated class="max-w-[110px] font-[550]" :title="(session.me?.roles ?? []).join(', ') || 'no roles'">{{ session.me?.username }}</Truncated>
        <UButton v-if="session.me?.authMode === 'oidc'" label="Sign out" color="neutral" variant="outline" size="xs" class="ms-auto" @click="logout" />
        <!-- Its own line under the name; wraps instead of overflowing. -->
        <p v-if="lasts" data-login-lasts class="m-0 basis-full text-xs break-words text-(--text-3)">{{ lasts }}</p>
      </div>
      <div v-if="about" class="text-xs break-words text-(--text-3)">
        <a v-if="about.href" :href="about.href" target="_blank" rel="noopener" data-about-version class="text-(--text-3) underline">{{ about.text }}</a>
        <span v-else data-about-version>{{ about.text }}</span>
        <p v-if="about.note" data-about-note class="m-0 text-(--tone-info-fg)">{{ about.note }}</p>
      </div>
    </div>
  </div>
</template>
