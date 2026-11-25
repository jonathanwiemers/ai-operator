<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, toRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { hasFeature, session, setFeatures } from './api'
import { getAbout, getFeatures, getInbox, getMe, getProjects, getProjectSets, getResearches, LoginRequiredError } from './client'
import { inboxKey, newNotifications, notificationText, notifyCategory } from './inboxNotify'
import { connectEvents, disconnectEvents, live } from './live'
import { notifyActive, notifyDialog, refreshPermission, wants } from './notify'
import { textEntry } from './textEntry'
import UApp from '@nuxt/ui/components/App.vue'
import UDashboardGroup from '@nuxt/ui/components/DashboardGroup.vue'
import { useBackClose } from './ui/useBackClose'
import { announcer, announce } from './ui/useAnnounce'
import { onRouteDone, useRouteFocus } from './ui/useRouteFocus'
import { inboxAnnouncement } from './announce'
import SignIn from './views/SignIn.vue'
import { crumbNames } from './crumbNames'
import { breadcrumbItems, crumbsFor, showsBreadcrumb } from './crumbs'
import { isRouteName } from './routeNames'
import { isPaletteKey, paletteHint, paletteOpen } from './searchPalette'
// The panel, the sidebar, the rail, the buttons, the ⌘K palette and the
// phone's bottom bar load after the shell, like the routes: they keep the
// initial bundle near its budget (docs/contributing/web-ui.md, "Bundle
// budget"). The sidebar loads its sheet, about 30 kB gzipped, only when it
// opens on a phone.
const UDashboardPanel = defineAsyncComponent(() => import('@nuxt/ui/components/DashboardPanel.vue'))
const UDashboardSidebar = defineAsyncComponent(() => import('@nuxt/ui/components/DashboardSidebar.vue'))
const UDashboardSidebarCollapse = defineAsyncComponent(() => import('@nuxt/ui/components/DashboardSidebarCollapse.vue'))
const UButton = defineAsyncComponent(() => import('@nuxt/ui/components/Button.vue'))
const UTooltip = defineAsyncComponent(() => import('@nuxt/ui/components/Tooltip.vue'))
const UBreadcrumb = defineAsyncComponent(() => import('@nuxt/ui/components/Breadcrumb.vue'))
const RailNav = defineAsyncComponent(() => import('./components/RailNav.vue'))
const SearchPalette = defineAsyncComponent(() => import('./components/SearchPalette.vue'))
const BottomTabBar = defineAsyncComponent(() => import('./ui/BottomTabBar.vue'))
const NotifySettings = defineAsyncComponent(() => import('./components/NotifySettings.vue'))
// The service worker's page side (deskWorker.ts): registration, the
// notifications and their clicks, the `?open=` query. It loads right after
// the shell.
const deskWorker = import('./deskWorker')
import type { InboxItem, ProjectSummary, ResearchSummary } from './types'
import type { ProjectSetSummary } from './generated/views'

async function loadMe(delay = 2000): Promise<void> {
  try {
    session.me ??= await getMe()
    setFeatures(await getFeatures())
    void loadAbout()
  } catch (e) {
    if (e instanceof LoginRequiredError) {
      setFeatures([])
    } else {
      window.setTimeout(() => void loadMe(Math.min(delay * 2, 60000)), delay)
    }
  }
}

// The running and the newest release; errors leave the last answer in place.
async function loadAbout(): Promise<void> {
  try {
    session.about = await getAbout()
  } catch {
    // The line is a hint; nothing depends on it.
  }
}
let aboutTimer: number | undefined

const router = useRouter()
// The breadcrumb (decision 5): only below the top level, where the page's
// header alone does not say where it sits.
const route = useRoute()
const crumbs = computed(() =>
  isRouteName(route.name) && showsBreadcrumb(route.name)
    ? breadcrumbItems(crumbsFor({ name: route.name, params: route.params, query: route.query }, crumbNames))
    : [],
)
// The rail sheet on phones (docs/contributing/web-ui.md, "Sheets"): back closes it, and a
// link inside it closes it before it navigates.
const railOpen = ref(false)
useBackClose(railOpen)
// Here, not in the palette: it mounts already open and would miss that.
useBackClose(paletteOpen)
const paletteUsed = ref(false)
watch(paletteOpen, (v) => (paletteUsed.value ||= v))
// The Notifications dialog, the same way: it loads on first open.
const notifyOpen = toRef(notifyDialog, 'open')
useBackClose(notifyOpen)
const notifyUsed = ref(false)
watch(notifyOpen, (v) => (notifyUsed.value ||= v))
// ⌘K / Ctrl+K opens and closes the palette, also inside text fields.
function onKey(e: KeyboardEvent) {
  if (!session.me || session.loginRequired || !isPaletteKey(e)) return
  e.preventDefault()
  paletteOpen.value = !paletteOpen.value
}
const projects = ref<ProjectSummary[] | null>(null)
// null until loaded and after a failed request: the rail shows the flat list.
const projectSets = ref<ProjectSetSummary[] | null>(null)
const researches = ref<ResearchSummary[] | null>(null)
const inboxCount = ref(0)
// Focus on the new page's h1 after a navigation, and the tab title.
useRouteFocus(router, inboxCount)
// The title is announced only when focus did not move to the h1, which a
// screen reader reads by itself (D640-P1), and only for a new page: a tab
// or filter on the same page keeps its title and the focus on its control.
onRouteDone(({ title, newPage, focused }) => {
  if (newPage && !focused) announce(title)
})
// Offline is announced once per drop of the event stream (D640-P3), not on
// sign-out, which ends the stream too.
watch(
  () => live.connected,
  (on, was) => {
    if (was && !on && session.me && !session.loginRequired) announce('Offline', { assertive: true })
  },
)

// The skip link moves focus to the page without a hash in the URL, which
// the router would see as a navigation.
function skipToMain() {
  document.getElementById('main')?.focus()
}

// Keys of the Inbox items this tab has seen. The first load while
// notifications are on only seeds them; later loads notify once per new item.
const seenInbox = new Set<string>()
let inboxSeeded = false
// The first load sets the count without announcing it.
let inboxLoaded = false

function onInbox(items: InboxItem[]) {
  if (inboxLoaded && items.length !== inboxCount.value) announce(inboxAnnouncement(items.length))
  inboxLoaded = true
  inboxCount.value = items.length
  refreshPermission()
  if (!notifyActive()) {
    inboxSeeded = false
    return
  }
  const fresh = inboxSeeded ? newNotifications(seenInbox, items) : []
  for (const item of items) seenInbox.add(inboxKey(item))
  inboxSeeded = true
  for (const item of fresh) {
    if (!wants(notifyCategory(item))) continue
    const { title, body } = notificationText(item)
    void deskWorker.then((w) => w.showDeskNotification(title, body, inboxKey(item), { item }))
  }
}

async function loadRail() {
  if (!session.me || session.loginRequired) return
  const jobs: Promise<void>[] = []
  if (hasFeature('factory')) {
    jobs.push(
      getProjects()
        .then((p) => {
          projects.value = p
        })
        .catch(() => undefined),
      getProjectSets()
        .then((s) => {
          projectSets.value = s
        })
        .catch(() => {
          projectSets.value = null
        }),
      getInbox()
        .then((r) => onInbox(r.items ?? []))
        .catch(() => undefined),
    )
  }
  if (hasFeature('research')) {
    jobs.push(
      getResearches()
        .then((r) => {
          researches.value = r
        })
        .catch(() => undefined),
    )
  }
  await Promise.all(jobs)
}

// The sheet is the phone's rail: it goes when the window widens to where
// the sidebar shows.
const phone = window.matchMedia('(max-width: 759.98px)')
const isPhone = ref(phone.matches)
function onWidth() {
  isPhone.value = phone.matches
  if (!phone.matches) railOpen.value = false
}

// While a text field has the focus the keyboard is up on a phone: the bottom
// bar goes, so the composers get the room.
const typing = ref(false)
function onFocusIn(e: FocusEvent) {
  typing.value = textEntry(e.target)
}
function onFocusOut(e: FocusEvent) {
  typing.value = textEntry(e.relatedTarget)
}

onMounted(() => {
  void loadMe()
  aboutTimer = window.setInterval(() => {
    if (document.visibilityState === 'visible' && session.features) void loadAbout()
  }, 5 * 60 * 1000)
  phone.addEventListener('change', onWidth)
  document.addEventListener('focusin', onFocusIn)
  document.addEventListener('focusout', onFocusOut)
  window.addEventListener('keydown', onKey)
  void deskWorker.then((w) => w.startWorker(router))
})
onBeforeUnmount(() => {
  window.clearInterval(aboutTimer)
  phone.removeEventListener('change', onWidth)
  document.removeEventListener('focusin', onFocusIn)
  document.removeEventListener('focusout', onFocusOut)
  window.removeEventListener('keydown', onKey)
})

watch(
  () => session.me !== null && !session.loginRequired,
  (ready) => {
    if (ready) {
      connectEvents()
      void loadRail()
    } else {
      disconnectEvents()
    }
  },
)

watch(
  () => session.features,
  () => {
    if (session.me && !session.loginRequired) void loadRail()
  },
)

watch(
  () => live.version,
  () => {
    if (session.loginRequired) return
    if (!live.kinds || live.kinds.some((k) => k === 'project' || k === 'task' || k === 'research')) {
      void loadRail()
    }
  },
)

// Turning notifications on seeds the seen keys right away, so only items
// added afterwards notify.
watch(notifyActive, (on) => {
  if (on) void loadRail()
})
</script>

<template>
  <UApp>
    <!-- The announcer's live regions (useAnnounce.ts), outside the sign-in
         switch so they stay in the page. -->
    <div class="sr-only" role="status" aria-live="polite" data-announce="polite">{{ announcer.polite }}</div>
    <div class="sr-only" role="alert" aria-live="assertive" data-announce="assertive">{{ announcer.assertive }}</div>
    <!-- The first focusable element, shown while it has focus. -->
    <a
      v-if="!session.loginRequired"
      href="#main"
      class="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:flex focus:min-h-11 focus:items-center focus:rounded-md focus:border focus:border-(--border) focus:bg-(--surface) focus:px-4 focus:font-semibold focus:text-(--text) focus:shadow-lg"
      @click.prevent="skipToMain"
      >Skip to content</a
    >
    <SignIn v-if="session.loginRequired" />
    <!-- The shell (docs/contributing/web-ui.md, "Navigation"): Nuxt UI's dashboard
         layout. From 760 px the sidebar is a column, below it a sheet; the
         group is fixed there and the panel scrolls. On phones the page
         scrolls, with the mobile bar on top and the bottom tab bar.
         --bottombar is the bottom bar's height above the bottom inset, its
         border included; 0 while the keyboard is up and the bar is gone
         (Composer.vue reads it). -->
    <UDashboardGroup
      v-else
      storage="local"
      storage-key="desk"
      :class="[
        'max-tablet:static max-tablet:min-h-dvh max-tablet:flex-col max-tablet:overflow-visible',
        typing ? 'max-tablet:[--bottombar:0px]' : 'max-tablet:[--bottombar:56px]',
      ]"
    >
      <!-- Phones: the brand and search on top, the navigation in the bottom bar. -->
      <header
        class="mobilebar sticky top-0 z-10 flex items-center gap-2.5 border-b border-(--border) bg-(--surface) ps-[max(16px,env(safe-area-inset-left))] pe-[max(16px,env(safe-area-inset-right))] pt-[calc(6px+env(safe-area-inset-top))] pb-1.5 font-bold tablet:hidden"
      >
        <!-- A link styled as a control: 44 px, like the buttons (style.css, "Hit areas"). -->
        <RouterLink to="/" class="brand-m flex min-h-11 items-center gap-2 text-(--text) hover:no-underline">
          <img src="/logo.svg" alt="" width="22" height="22" />
          ai-operator
        </RouterLink>
        <UButton
          icon="i-lucide-search"
          color="neutral"
          variant="ghost"
          aria-label="Search"
          aria-haspopup="dialog"
          class="ms-auto size-11 justify-center"
          :ui="{ leadingIcon: 'size-5' }"
          @click="paletteOpen = true"
        />
      </header>
      <BottomTabBar v-if="isPhone" v-show="!typing" :inbox-count="inboxCount" :menu-open="railOpen" @menu="railOpen = true" />

      <!-- The rail: a column from 760 px, collapsible, its state in
           localStorage (desk-sidebar-rail). Below 760 px a sheet from the left
           (docs/contributing/web-ui.md, "Sheets"): its links navigate and close
           it, useBackClose leaves no history entry. -->
      <UDashboardSidebar
        id="rail"
        v-model:open="railOpen"
        class="rail"
        mode="slideover"
        collapsible
        toggle-side="right"
        :menu="{ title: 'ai-operator', description: 'Projects, research and engines' }"
        :ui="{
          // Nuxt UI switches at lg (1024 px); the Desk at 760 (tablet:).
          root: 'group z-20 min-h-0 w-62 bg-(--surface) pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] ps-[env(safe-area-inset-left)] tablet:flex data-[collapsed=true]:w-16',
          header: 'h-auto gap-1 px-0 pe-2.5 sm:px-0 sm:pe-2.5 group-data-[collapsed=true]:flex-col group-data-[collapsed=true]:pe-0 group-data-[collapsed=true]:pb-2',
          body: 'min-h-0 gap-0 overflow-hidden p-0 sm:p-0',
          // Above the sticky mobile bar and the bottom bar (z-index 10).
          overlay: 'z-40 tablet:hidden',
          content: 'z-40 w-[min(280px,88vw)] pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] ps-[env(safe-area-inset-left)] tablet:hidden',
        }"
      >
        <template #header="{ collapsed }">
          <RouterLink
            to="/"
            class="flex items-center gap-2.5 px-4 pt-3.5 pb-2.5 text-[15px] font-bold text-(--text) hover:no-underline"
            :aria-label="collapsed ? 'ai-operator' : undefined"
          >
            <img src="/logo.svg" alt="" width="24" height="24" class="rounded-[7px]" />
            <span v-if="!collapsed">ai-operator</span>
          </RouterLink>
          <template v-if="!isPhone">
            <UTooltip :text="`Search (${paletteHint})`">
              <UButton
                icon="i-lucide-search"
                color="neutral"
                variant="ghost"
                :aria-label="`Search (${paletteHint})`"
                aria-haspopup="dialog"
                :class="collapsed ? undefined : 'ms-auto'"
                @click="paletteOpen = true"
              />
            </UTooltip>
            <UDashboardSidebarCollapse
              class="tablet:flex"
              :icon="collapsed ? 'i-lucide-panel-left-open' : 'i-lucide-panel-left-close'"
            />
          </template>
        </template>
        <template #default="{ collapsed }">
          <RailNav
            :projects="projects"
            :project-sets="projectSets"
            :researches="researches"
            :inbox-count="inboxCount"
            :collapsed="collapsed"
            @navigate="railOpen = false"
          />
        </template>
      </UDashboardSidebar>

      <SearchPalette v-if="paletteUsed" :projects="projects" :features="session.features" :fullscreen="isPhone" />
      <NotifySettings v-if="notifyUsed" />

      <!-- The page; last in the group, so Nuxt UI draws no border after it.
           Each view fills the pane's height (flex: 1), the crumb bar keeps its own. -->
      <UDashboardPanel id="page" :ui="{ root: 'min-h-0' }">
        <main
          id="main"
          tabindex="-1"
          class="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto pt-[env(safe-area-inset-top)] pe-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] *:min-h-0 *:flex-1 max-tablet:overflow-visible max-tablet:pt-0 max-tablet:ps-[env(safe-area-inset-left)] max-tablet:pb-[calc(var(--bottombar)+env(safe-area-inset-bottom))]"
        >
          <!-- A div: the mobile bar is the only header, UBreadcrumb has its own nav. -->
          <div v-if="crumbs.length" class="flex-none! px-6 pt-2.5 max-tablet:px-4 max-tablet:pt-2">
            <!-- One line: phones show the first and the last crumb; each label is cut at 28ch. -->
            <UBreadcrumb
              :items="crumbs"
              :ui="{ list: 'min-w-0 gap-1', link: 'text-[13px]', linkLabel: 'max-w-[28ch]', separatorIcon: 'size-4' }"
            />
          </div>
          <RouterView />
        </main>
      </UDashboardPanel>
    </UDashboardGroup>
  </UApp>
</template>
