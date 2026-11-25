<script setup lang="ts">
// The rail's content (docs/contributing/web-ui.md, "Navigation"): New plan
// at the top (with the plans feature; collapsed an icon with a tooltip), a
// vertical UNavigationMenu with Inbox, Overview, Metrics, Plans, the projects with
// their sets and the research (src/railItems.ts), then the engine meters with
// their reset times and, at the very bottom, the account button: avatar,
// name and live dot, opening the account menu (src/accountMenu.ts) with the
// login, Appearance, Notifications, the Desk version and Sign out. App.vue renders it in the
// sidebar on tablet and desktop and in the rail sheet on phones; App.vue
// loads the data. Collapsed (the sidebar at 64 px) it shows the pages as
// icons with their names as tooltips, no meters, and the account button as
// avatar and dot.
import { computed, inject, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { hasFeature, session } from '../api'
import { logout } from '../client'
import { aboutLine } from '../about'
import { accountItems, initials } from '../accountMenu'
import { themeInjectionKey, type Theme } from '../colorScheme'
import { absTime, loginLasts, resetLabel, windowLabel } from '../format'
import { live, useNow } from '../live'
import { openNotifyDialog } from '../notify'
import { openSets, railItems, railNewPlan } from '../railItems'
import type { EngineUsage, ProjectSummary, ResearchSummary } from '../types'
import type { ProjectSetSummary } from '../generated/views'
import UAvatar from '@nuxt/ui/components/Avatar.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UDropdownMenu from '@nuxt/ui/components/DropdownMenu.vue'
import UNavigationMenu from '@nuxt/ui/components/NavigationMenu.vue'
import UProgress from '@nuxt/ui/components/Progress.vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import RelTime from './RelTime.vue'

const props = defineProps<{
  projects: ProjectSummary[] | null
  // null when not loaded or the request failed: the flat list as before.
  projectSets?: ProjectSetSummary[] | null
  researches: ResearchSummary[] | null
  inboxCount: number
  // The sidebar is collapsed (App.vue): the pages as icons, the account
  // button as avatar and dot, the meters hidden.
  collapsed?: boolean
}>()

// navigate: a link was selected. The sheet closes on it, also for a link to
// the page it is on, which the router does not navigate to again.
const emit = defineEmits<{ navigate: [] }>()

const route = useRoute()
const now = useNow()
const about = computed(() => (session.about ? aboutLine(session.about) : null))
const lasts = computed(() => (session.me?.authMode === 'oidc' ? loginLasts(session.me, Date.now()) : null))

// The theme goes through the controller main.ts provides (colorScheme.ts),
// which sets the `dark` class and keeps the choice in this browser.
const themes = inject(themeInjectionKey, null)
const theme = ref<Theme>(themes?.get() ?? 'auto')
function setTheme(t: Theme) {
  theme.value = t
  themes?.set(t)
}
const account = computed(() =>
  accountItems({
    me: session.me ?? null,
    lasts: lasts.value,
    about: about.value,
    theme: theme.value,
    setTheme,
    openNotifications: openNotifyDialog,
    signOut: () => void logout(),
  }),
)
const username = computed(() => session.me?.username ?? '')
const liveText = computed(() => (live.connected ? 'Live' : 'Offline'))
const liveHint = computed(() => (live.connected ? 'Updates arrive live' : 'Reconnecting…'))
const dot = computed(() => (live.connected ? 'bg-(--tone-success-fg)' : 'bg-(--text-3)'))

const features = computed(() => ({ history: hasFeature('history'), plans: hasFeature('plans'), research: hasFeature('research') }))
const items = computed(() =>
  railItems({
    path: route.path,
    inboxCount: props.inboxCount,
    projects: props.projects,
    projectSets: props.projectSets,
    researches: props.researches,
    features: features.value,
    collapsed: props.collapsed,
    onSelect: () => emit('navigate'),
  }),
)
const newPlan = computed(() => railNewPlan({ features: features.value }))

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
  return [...byEngine.values()].flatMap((u) =>
    (u.windows ?? []).map((w) => ({
      key: `${u.engine} ${w.name}`,
      label: `${u.engine} ${windowLabel(w.name)}`,
      percent: w.percent,
      reset: resetLabel(w.resetsAt, now.value),
      resetTitle: absTime(w.resetsAt),
    })),
  )
})
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col">
    <!-- New plan, above the navigation; navigate closes the phone sheet.
         44 px on phones, like the links. -->
    <div v-if="newPlan" :class="['px-2 pt-2', collapsed && 'flex justify-center']">
      <UTooltip :text="newPlan.label" :disabled="!collapsed" :content="{ side: 'right' }">
        <UButton
          color="primary"
          :to="newPlan.to"
          :icon="newPlan.icon"
          :label="collapsed ? undefined : newPlan.label"
          :aria-label="collapsed ? newPlan['aria-label'] : undefined"
          :block="!collapsed"
          :square="collapsed"
          data-rail-new-plan
          class="max-tablet:min-h-11 max-tablet:min-w-11"
          @click="emit('navigate')"
        />
      </UTooltip>
    </div>
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


    <div v-if="!collapsed && engineMeters.length" class="rail-foot grid gap-1.5 border-t border-(--border) px-3.5 py-2.5 text-xs text-(--text-2)">
      <div v-for="m in engineMeters" :key="m.key" data-usage-meter class="grid grid-cols-[72px_1fr_34px] items-center gap-x-2 gap-y-0.5 text-[11.5px] whitespace-nowrap">
        <span>{{ m.label }}</span>
        <UProgress :model-value="Math.min(100, m.percent)" size="xs" :color="m.percent >= 80 ? 'warning' : 'info'" :aria-label="m.label" />
        <span>{{ m.percent }}%</span>
        <!-- When the window resets, under the bar; muted once past (the
             EngineUsage.vue rule). -->
        <span v-if="m.reset" data-usage-reset class="col-span-2 col-start-2 text-[11px]" :class="m.reset.past && 'text-(--text-3)'" :title="m.resetTitle">{{ m.reset.text }}</span>
      </div>
    </div>

    <!-- The account button, also when collapsed: the menu opens upward, or
         to the right of the 64 px sidebar. Its name carries the live state,
         which the dot shows. Collapsed, the 36 px button ends 14 px before
         the sidebar's edge: the offset puts the menu 8 px beyond it. -->
    <div class="border-t border-(--border)" :class="collapsed ? 'flex justify-center p-2' : 'px-2 py-1.5'">
      <UDropdownMenu
        :items="account"
        :content="{ side: collapsed ? 'right' : 'top', align: collapsed ? 'end' : 'start', sideOffset: collapsed ? 22 : 8 }"
        :ui="{ content: 'z-40 w-60 max-w-[90vw]', item: 'max-tablet:min-h-11' }"
      >
        <UTooltip :text="`${username} · ${liveHint}`" :disabled="!collapsed" :content="{ side: 'right' }">
          <UButton
            color="neutral"
            variant="ghost"
            :block="!collapsed"
            :aria-label="`Account, ${username}, ${liveText}`"
            data-account-button
            :data-live="liveText"
            :class="['max-tablet:min-h-11', collapsed ? 'p-1.5' : 'justify-start']"
          >
            <span class="relative inline-flex shrink-0">
              <UAvatar :text="initials(username)" size="xs" aria-hidden="true" />
              <span v-if="collapsed" :class="['absolute -end-0.5 -bottom-0.5 size-2 rounded-full ring-2 ring-(--surface)', dot]" />
            </span>
            <template v-if="!collapsed">
              <span class="min-w-0 flex-1 truncate text-start font-[550]">{{ username }}</span>
              <UTooltip :text="liveHint">
                <span :class="['me-1 size-2 shrink-0 rounded-full', dot, live.connected && 'ring-3 ring-(--tone-success-bg)']" />
              </UTooltip>
            </template>
          </UButton>
        </UTooltip>

        <template #who="{ item }">
          <span class="grid min-w-0 gap-0.5 font-normal">
            <span class="truncate font-semibold text-(--text)">{{ item.label }}</span>
            <span class="text-(--text-3)">{{ item.roles }}</span>
            <span v-if="item.lasts" data-login-lasts class="break-words text-(--text-3)">{{ item.lasts }}</span>
          </span>
        </template>
        <template #theme-label="{ item }">
          <span :data-theme="item.theme">{{ item.label }}</span>
        </template>
        <template #about-label="{ item }">
          <span data-about-version :title="item.description">{{ item.label }}</span>
        </template>
        <template #about-description="{ item }">
          <span data-about-note class="text-(--tone-info-fg)">{{ item.description }}</span>
        </template>
      </UDropdownMenu>
    </div>
  </div>
</template>
