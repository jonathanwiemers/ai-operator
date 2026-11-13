<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { hasFeature, isResearchPath, session, setFeatures } from './api'
import { getAbout, getFeatures, getInbox, getMe, getProjects, getResearches, LoginRequiredError } from './client'
import { deskTitle } from './format'
import { inboxKey, newNotifications, notificationText } from './inboxNotify'
import { inboxRoute } from './inboxRoute'
import { connectEvents, disconnectEvents, live } from './live'
import { notifyActive, refreshPermission } from './notify'
import { textEntry } from './textEntry'
import UApp from '@nuxt/ui/components/App.vue'
import UIcon from '@nuxt/ui/components/Icon.vue'
import { useBackClose } from './ui/useBackClose'
import SignIn from './views/SignIn.vue'
import Breadcrumbs from './components/Breadcrumbs.vue'
import RailNav from './components/RailNav.vue'
import { isPaletteKey, paletteHint, paletteOpen } from './searchPalette'
// The ⌘K palette loads on its first open (docs/ui.md, "Bundle budget").
const SearchPalette = defineAsyncComponent(() => import('./components/SearchPalette.vue'))
// The sheet is loaded on phones only: the dialog behind it would add about
// 30 kB gzipped to the initial bundle (docs/ui.md, "Bundle budget").
const USlideover = defineAsyncComponent(() => import('@nuxt/ui/components/Slideover.vue'))
import type { InboxItem, ProjectSummary, ResearchSummary } from './types'

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
// The rail sheet on phones (docs/ui.md, "Sheets"): back closes it, and a
// link inside it closes it before it navigates.
const railOpen = ref(false)
useBackClose(railOpen)
// Here, not in the palette: it mounts already open and would miss that.
useBackClose(paletteOpen)
const paletteUsed = ref(false)
watch(paletteOpen, (v) => (paletteUsed.value ||= v))
// ⌘K / Ctrl+K opens and closes the palette, also inside text fields.
function onKey(e: KeyboardEvent) {
  if (!session.me || session.loginRequired || !isPaletteKey(e)) return
  e.preventDefault()
  paletteOpen.value = !paletteOpen.value
}
const projects = ref<ProjectSummary[] | null>(null)
const researches = ref<ResearchSummary[] | null>(null)
const inboxCount = ref(0)
const baseTitle = document.title
watch(inboxCount, (n) => { document.title = deskTitle(n, baseTitle) }, { immediate: true })

// Keys of the Inbox items this tab has seen. The first load while
// notifications are on only seeds them; later loads notify once per new item.
const seenInbox = new Set<string>()
let inboxSeeded = false

function onInbox(items: InboxItem[]) {
  inboxCount.value = items.length
  refreshPermission()
  if (!notifyActive()) {
    inboxSeeded = false
    return
  }
  const fresh = inboxSeeded ? newNotifications(seenInbox, items) : []
  for (const item of items) seenInbox.add(inboxKey(item))
  inboxSeeded = true
  for (const item of fresh) showNotification(item)
}

function showNotification(item: InboxItem) {
  const { title, body } = notificationText(item)
  try {
    const n = new Notification(title, { body, tag: inboxKey(item), icon: '/logo.svg' })
    n.onclick = () => {
      window.focus()
      void router.push(inboxRoute(item))
      n.close()
    }
  } catch {
    // Browsers that show notifications only from a service worker.
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
// the aside shows.
const phone = window.matchMedia('(max-width: 759.98px)')
const isPhone = ref(phone.matches)
function onWidth() {
  isPhone.value = phone.matches
  if (!phone.matches) railOpen.value = false
}

// The phone's bottom bar (docs/ui.md, "Navigation") marks the section of the
// current page with aria-current.
const route = useRoute()
const section = computed(() => {
  if (route.name === 'inbox') return 'inbox'
  if (route.name === 'overview') return 'overview'
  if (route.name === 'costs') return 'costs'
  if (isResearchPath(route.path)) return 'research'
  return null
})
const current = (s: string) => (section.value === s ? 'page' : undefined)

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
    <SignIn v-if="session.loginRequired" />
    <div v-else class="app" :class="{ typing }">
      <!-- Phones: the brand on top, the navigation in the bottom bar. -->
      <header class="mobilebar">
        <RouterLink to="/" class="brand-m">
          <img src="/logo.svg" alt="" width="22" height="22" />
          ai-operator
        </RouterLink>
        <button type="button" class="searchbtn searchbtn-m" aria-label="Search" aria-haspopup="dialog" @click="paletteOpen = true">
          <UIcon name="i-lucide-search" class="size-5" />
        </button>
      </header>
      <nav class="bottombar" aria-label="Sections">
        <RouterLink
          to="/inbox"
          class="tab"
          :aria-current="current('inbox')"
          :aria-label="inboxCount ? `Inbox (${inboxCount})` : undefined"
        >
          <span class="tab-icon">
            <UIcon name="i-lucide-inbox" class="size-5" />
            <span v-if="inboxCount" class="count">{{ inboxCount }}</span>
          </span>
          Inbox
        </RouterLink>
        <RouterLink to="/" class="tab" :aria-current="current('overview')">
          <UIcon name="i-lucide-layout-dashboard" class="size-5" />
          Overview
        </RouterLink>
        <button type="button" class="tab" aria-haspopup="dialog" :aria-expanded="railOpen" @click="railOpen = true">
          <UIcon name="i-lucide-folders" class="size-5" />
          Projects
        </button>
        <RouterLink v-if="hasFeature('history')" to="/costs" class="tab" :aria-current="current('costs')">
          <UIcon name="i-lucide-receipt" class="size-5" />
          Costs
        </RouterLink>
        <RouterLink v-if="hasFeature('research')" to="/research" class="tab" :aria-current="current('research')">
          <UIcon name="i-lucide-telescope" class="size-5" />
          Research
        </RouterLink>
      </nav>

      <aside class="rail">
        <div class="railhead">
          <RouterLink to="/" class="brand">
            <img src="/logo.svg" alt="" width="24" height="24" />
            <span>ai-operator</span>
          </RouterLink>
          <button
            type="button"
            class="searchbtn"
            :aria-label="`Search (${paletteHint})`"
            :title="`Search (${paletteHint})`"
            aria-haspopup="dialog"
            @click="paletteOpen = true"
          >
            <UIcon name="i-lucide-search" class="size-4" />
          </button>
        </div>
        <RailNav :projects="projects" :researches="researches" :inbox-count="inboxCount" />
      </aside>

      <!-- Phones: the rail as a sheet from the left (docs/ui.md, "Sheets").
           Its links navigate and close it; useBackClose leaves no entry. -->
      <USlideover
        v-if="isPhone"
        v-model:open="railOpen"
        side="left"
        title="ai-operator"
        description="Projects, research and engines"
        close-icon="i-lucide-x"
        :ui="{
          // Above the sticky mobile bar (z-index 10); Nuxt UI sets none.
          overlay: 'z-40',
          content:
            'z-40 w-[min(280px,88vw)] pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)]',
          header: 'min-h-0 gap-2 py-1 ps-4 pe-1 sm:ps-4 sm:pe-1',
          title: 'text-[15px] font-bold',
          description: 'sr-only',
          close: 'static ms-auto',
          body: 'flex min-h-0 flex-col overflow-hidden p-0 pt-2 sm:p-0 sm:pt-2',
        }"
      >
        <template #body>
          <RailNav :projects="projects" :researches="researches" :inbox-count="inboxCount" @navigate="railOpen = false" />
        </template>
      </USlideover>

      <SearchPalette v-if="paletteUsed" :projects="projects" :features="session.features" :fullscreen="isPhone" />

      <main class="main">
        <header class="crumbbar"><Breadcrumbs /></header>
        <RouterView />
      </main>
    </div>
  </UApp>
</template>

<style scoped>
.app {
  display: grid;
  grid-template-columns: 248px minmax(0, 1fr);
  /* dvh follows the browser's bars and, with interactive-widget=
     resizes-content (index.html), the keyboard. 100vh for browsers without
     it (docs/design.md, "Phones"). */
  height: 100vh;
  height: 100dvh;
  min-height: 0;
}
/* Safe areas (viewport-fit=cover): env() is 0 but on notched screens, so
   desktop is unchanged. The rail holds the left edge, the pane the rest. */
.rail {
  padding: env(safe-area-inset-top) 0 env(safe-area-inset-bottom) env(safe-area-inset-left);
  border-right: 1px solid var(--border);
  background: var(--surface);
  display: flex;
  flex-direction: column;
  min-height: 0;
  z-index: 20;
}
.railhead {
  display: flex;
  align-items: center;
  padding-right: 10px;
}
.searchbtn {
  margin-inline-start: auto;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px;
  border: 0;
  border-radius: 6px;
  background: none;
  color: var(--text-2);
  cursor: pointer;
}
.searchbtn:hover {
  background: var(--surface-2);
  color: var(--text);
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 16px 10px;
  font-weight: 700;
  font-size: 15px;
  color: var(--text);
}
.brand:hover {
  text-decoration: none;
}
.brand img {
  border-radius: 7px;
}
.count {
  font-size: 11px;
  font-weight: 700;
  min-width: 18px;
  text-align: center;
  padding: 1px 6px;
  border-radius: 9px;
  background: var(--accent);
  color: var(--on-accent);
}
.main {
  min-width: 0;
  min-height: 0;
  overflow: auto;
  display: flex;
  flex-direction: column;
  padding: env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) 0;
}
.main > :deep(*) {
  flex: 1;
  min-height: 0;
}
.main > .crumbbar {
  flex: none;
  padding: 10px 24px 0;
}
.mobilebar,
.bottombar {
  display: none;
}

@media (max-width: 759.98px) {
  .app {
    /* The bottom bar's height above the bottom inset, its border included. */
    --bottombar: 56px;
    grid-template-columns: minmax(0, 1fr);
    /* The bar keeps its height, the pane takes what is left: with two auto
       rows a short page would stretch the bar. */
    grid-template-rows: auto 1fr;
    height: auto;
    min-height: 100vh;
    min-height: 100dvh;
  }
  /* The mobile bar holds the top edge, so the pane's top inset goes; the
     pane's end clears the bottom bar. */
  .main {
    padding: 0 env(safe-area-inset-right) calc(var(--bottombar) + env(safe-area-inset-bottom))
      env(safe-area-inset-left);
    /* The pane grows with its content and the page scrolls. visible, not
       auto: a pane that never scrolls would still hold its sticky children
       (the chat composer) instead of the viewport. */
    overflow: visible;
  }
  /* A text field has the focus, so the keyboard is up: the bottom bar goes
     and the composer gets its room. */
  .app.typing {
    --bottombar: 0px;
  }
  .app.typing .bottombar {
    display: none;
  }
  /* The rail is the sheet on phones. */
  .rail {
    display: none;
  }
  .mobilebar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: calc(6px + env(safe-area-inset-top)) max(16px, env(safe-area-inset-right)) 6px
      max(16px, env(safe-area-inset-left));
    border-bottom: 1px solid var(--border);
    background: var(--surface);
    font-weight: 700;
    position: sticky;
    top: 0;
    z-index: 10;
  }
  .bottombar {
    display: flex;
    position: fixed;
    inset: auto 0 0;
    z-index: 10;
    padding: 0 env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);
    border-top: 1px solid var(--border);
    background: var(--surface);
  }
  /* Each item fills its share of the bar, the whole height a hit area; the
     bar's border takes the last pixel of --bottombar. */
  .tab {
    flex: 1;
    min-width: 0;
    height: calc(var(--bottombar) - 1px);
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 2px;
    border: 0;
    background: none;
    font: inherit;
    font-size: 11px;
    font-weight: 600;
    color: var(--text-2);
    cursor: pointer;
  }
  .tab:hover {
    text-decoration: none;
  }
  .tab[aria-current='page'] {
    color: var(--accent);
  }
  .tab-icon {
    position: relative;
    display: flex;
  }
  .tab-icon .count {
    position: absolute;
    top: -6px;
    left: 12px;
  }
  /* The brand is a link styled as a control: 44 px, like the buttons
     (style.css, "Hit areas"). */
  .brand-m {
    min-height: 44px;
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--text);
  }
  .brand-m:hover {
    text-decoration: none;
  }
  .searchbtn-m {
    width: 44px;
    height: 44px;
    justify-content: center;
    padding: 0;
  }
  .main > .crumbbar {
    padding: 8px 16px 0;
  }
}
</style>
