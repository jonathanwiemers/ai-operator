<script setup lang="ts">
import { defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { get, hasFeature, session, setFeatures, LoginRequiredError } from './api'
import { deskTitle } from './format'
import { inboxKey, newNotifications, notificationText } from './inboxNotify'
import { inboxRoute } from './inboxRoute'
import { connectEvents, disconnectEvents, live } from './live'
import { notifyActive, refreshPermission } from './notify'
import UApp from '@nuxt/ui/components/App.vue'
import { useBackClose } from './ui/useBackClose'
import SignIn from './views/SignIn.vue'
import Breadcrumbs from './components/Breadcrumbs.vue'
import RailNav from './components/RailNav.vue'
// The sheet is loaded on phones only: the dialog behind it would add about
// 30 kB gzipped to the initial bundle (docs/ui.md, "Bundle budget").
const USlideover = defineAsyncComponent(() => import('@nuxt/ui/components/Slideover.vue'))
import type { Feature, InboxItem, InboxResponse, Me, ProjectSummary, ResearchSummary } from './types'

async function loadMe(delay = 2000): Promise<void> {
  try {
    session.me ??= await get<Me>('/me')
    setFeatures((await get<{ features: Feature[] }>('/features')).features)
  } catch (e) {
    if (e instanceof LoginRequiredError) {
      setFeatures([])
    } else {
      window.setTimeout(() => void loadMe(Math.min(delay * 2, 60000)), delay)
    }
  }
}

const router = useRouter()
// The rail sheet on phones (docs/ui.md, "Sheets"): back closes it, and a
// link inside it closes it before it navigates.
const railOpen = ref(false)
useBackClose(railOpen)
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
      get<ProjectSummary[]>('/projects')
        .then((p) => {
          projects.value = p
        })
        .catch(() => undefined),
      get<InboxResponse>('/inbox')
        .then((r) => onInbox(r.items ?? []))
        .catch(() => undefined),
    )
  }
  if (hasFeature('research')) {
    jobs.push(
      get<ResearchSummary[]>('/researches')
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

onMounted(() => {
  void loadMe()
  phone.addEventListener('change', onWidth)
})
onBeforeUnmount(() => phone.removeEventListener('change', onWidth))

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
    <div v-else class="app">
      <header class="mobilebar">
        <button
          type="button"
          class="menu"
          aria-label="Open menu"
          aria-haspopup="dialog"
          :aria-expanded="railOpen"
          @click="railOpen = true"
        >
          ☰
        </button>
        <RouterLink to="/" class="brand-m">
          <img src="/logo.svg" alt="" width="22" height="22" />
          ai-operator
        </RouterLink>
        <span v-if="inboxCount" class="count">{{ inboxCount }}</span>
      </header>

      <aside class="rail">
        <RouterLink to="/" class="brand">
          <img src="/logo.svg" alt="" width="24" height="24" />
          <span>ai-operator</span>
        </RouterLink>
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
  color: #fff;
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
.mobilebar {
  display: none;
}

@media (max-width: 759.98px) {
  .app {
    grid-template-columns: minmax(0, 1fr);
    /* The bar keeps its height, the pane takes what is left: with two auto
       rows a short page would stretch the bar. */
    grid-template-rows: auto 1fr;
    height: auto;
    min-height: 100vh;
    min-height: 100dvh;
  }
  /* The bar holds the top edge, so the pane's top inset goes. */
  .main {
    padding: 0 env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);
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
  .menu {
    border: 1px solid var(--border);
    background: var(--surface-2);
    border-radius: 6px;
    width: 44px;
    height: 44px;
    cursor: pointer;
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
  .mobilebar .count {
    margin-left: auto;
  }
  .main > .crumbbar {
    padding: 8px 16px 0;
  }
}
</style>
