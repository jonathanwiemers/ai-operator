<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { get, hasFeature, logout, session, setFeatures, LoginRequiredError } from './api'
import { deskTitle, windowLabel } from './format'
import { inboxKey, newNotifications, notificationText } from './inboxNotify'
import { inboxRoute } from './inboxRoute'
import { connectEvents, disconnectEvents, live } from './live'
import { notifyActive, refreshPermission } from './notify'
import { projectDot, projectNeedsYou, projectStateLine } from './stream'
import SignIn from './views/SignIn.vue'
import Breadcrumbs from './components/Breadcrumbs.vue'
import type { EngineUsage, Feature, InboxItem, InboxResponse, Me, ProjectSummary, ResearchSummary } from './types'

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

const route = useRoute()
const router = useRouter()
const railOpen = ref(false)
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

const engineMeters = computed(() => {
  const byEngine = new Map<string, EngineUsage>()
  for (const p of projects.value ?? []) {
    for (const u of p.engineUsage ?? []) {
      const prev = byEngine.get(u.engine)
      if (!prev || Date.parse(u.observedAt) > Date.parse(prev.observedAt)) byEngine.set(u.engine, u)
    }
  }
  return [...byEngine.values()]
})

function researchSub(r: ResearchSummary): string {
  if (r.phase === 'Running') return 'running'
  if (r.schedule) return r.schedule
  return r.oneOff ? 'one-off' : ''
}

function closeRail() {
  railOpen.value = false
}

function projectActive(namespace: string, name: string) {
  return route.params.namespace === namespace && route.params.name === name && route.path.startsWith('/projects/')
}

onMounted(() => loadMe())

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

watch(
  () => route.fullPath,
  () => closeRail(),
)
</script>

<template>
  <SignIn v-if="session.loginRequired" />
  <div v-else class="app" :class="{ 'rail-open': railOpen }">
    <div v-if="railOpen" class="scrim" @click="closeRail" />
    <header class="mobilebar">
      <button type="button" class="menu" aria-label="Open menu" @click="railOpen = true">☰</button>
      <RouterLink to="/" class="brand-m" @click="closeRail">
        <img src="/logo.svg" alt="" width="22" height="22" />
        ai-operator
      </RouterLink>
      <span v-if="inboxCount" class="count">{{ inboxCount }}</span>
    </header>

    <aside class="rail">
      <RouterLink to="/" class="brand" @click="closeRail">
        <img src="/logo.svg" alt="" width="24" height="24" />
        <span>ai-operator</span>
      </RouterLink>
      <nav>
        <RouterLink to="/inbox" class="item" @click="closeRail">
          <span class="dot" :class="{ warn: inboxCount > 0 }" />
          <span class="name">Inbox<span class="sub">needs you, across projects</span></span>
          <span v-if="inboxCount" class="count">{{ inboxCount }}</span>
        </RouterLink>
        <RouterLink to="/" class="item" active-class="active" @click="closeRail">
          <span class="dot" />
          <span class="name">Everything<span class="sub">all streams, newest first</span></span>
        </RouterLink>

        <div class="sect"><span>Projects</span></div>
        <RouterLink
          v-for="p in projects ?? []"
          :key="p.namespace + '/' + p.name"
          :to="{ name: 'stream-project', params: { namespace: p.namespace, name: p.name } }"
          class="item"
          :class="{ active: projectActive(p.namespace, p.name) }"
          active-class=""
          @click="closeRail"
        >
          <span
            class="dot"
            :class="{
              run: projectDot(p.stages) === 'running',
              warn: projectDot(p.stages) === 'needs-you',
              ok: projectDot(p.stages) === 'idle',
            }"
          />
          <span class="name">
            {{ p.name }}
            <span class="sub">{{ projectStateLine(p.stages, p.autopilots) }}</span>
          </span>
          <span v-if="projectNeedsYou(p.stages)" class="count">{{ projectNeedsYou(p.stages) }}</span>
        </RouterLink>
        <div v-if="projects && !projects.length" class="empty-rail">No projects</div>

        <template v-if="hasFeature('research')">
          <div class="sect"><span>Research</span></div>
          <RouterLink
            v-for="r in researches ?? []"
            :key="r.namespace + '/' + r.name"
            :to="`/research/${encodeURIComponent(r.namespace)}/${encodeURIComponent(r.name)}`"
            class="item"
            @click="closeRail"
          >
            <span class="dot" :class="{ run: r.phase === 'Running', ok: r.phase === 'Succeeded' }" />
            <span class="name">{{ r.name }}<span class="sub">{{ researchSub(r) }}</span></span>
          </RouterLink>
          <RouterLink to="/research" class="item soft" @click="closeRail">
            <span class="name">All research</span>
          </RouterLink>
        </template>
      </nav>

      <div class="rail-foot">
        <div v-for="u in engineMeters" :key="u.engine" class="meters">
          <div v-for="w in u.windows ?? []" :key="w.name" class="meter">
            <span>{{ u.engine }} {{ windowLabel(w.name) }}</span>
            <span class="bar">
              <i :class="{ hot: w.percent >= 80 }" :style="{ width: Math.min(100, w.percent) + '%' }" />
            </span>
            <span>{{ w.percent }}%</span>
          </div>
        </div>
        <div class="who">
          <span
            class="live"
            :class="{ on: live.connected }"
            :title="live.connected ? 'Updates arrive live' : 'Reconnecting…'"
          >
            <span class="ldot" />{{ live.connected ? 'Live' : 'Offline' }}
          </span>
          <span class="uname" :title="(session.me?.roles ?? []).join(', ') || 'no roles'">{{ session.me?.username }}</span>
          <button v-if="session.me?.authMode === 'oidc'" class="btn" type="button" @click="logout">Sign out</button>
        </div>
      </div>
    </aside>

    <main class="main">
      <header class="crumbbar"><Breadcrumbs /></header>
      <RouterView />
    </main>
  </div>
</template>

<style scoped>
.app {
  display: grid;
  grid-template-columns: 248px minmax(0, 1fr);
  height: 100vh;
  min-height: 0;
}
.rail {
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
nav {
  overflow-y: auto;
  flex: 1;
  padding: 0 8px 12px;
}
.sect {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-3);
  padding: 14px 8px 6px;
  font-weight: 600;
}
.item {
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 7px 8px;
  border-radius: var(--radius-sm);
  color: var(--text-2);
  position: relative;
}
.item:hover {
  background: var(--surface-2);
  text-decoration: none;
}
.item.active,
.item.router-link-active:not(.soft) {
  background: var(--surface-3);
  color: var(--text);
  font-weight: 600;
}
.item.soft {
  font-size: 12.5px;
  color: var(--text-3);
}
.item .name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.item .sub {
  display: block;
  font-size: 11.5px;
  font-weight: 400;
  color: var(--text-3);
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--border-strong);
  flex: none;
}
.dot.run {
  background: var(--tone-progress-fg);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--tone-progress-fg) 22%, transparent);
  animation: pulse 1.6s infinite;
}
.dot.ok {
  background: var(--tone-success-fg);
}
.dot.warn {
  background: var(--tone-warning-fg);
}
@keyframes pulse {
  50% {
    box-shadow: 0 0 0 6px transparent;
  }
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
.rail-foot {
  border-top: 1px solid var(--border);
  padding: 10px 14px;
  font-size: 12px;
  color: var(--text-2);
  display: grid;
  gap: 6px;
}
.meter {
  display: grid;
  grid-template-columns: 72px 1fr 34px;
  white-space: nowrap;
  align-items: center;
  gap: 8px;
  font-size: 11.5px;
}
.bar {
  height: 5px;
  border-radius: 3px;
  background: var(--surface-3);
  overflow: hidden;
}
.bar i {
  display: block;
  height: 100%;
  background: var(--tone-progress-fg);
  border-radius: 3px;
}
.bar i.hot {
  background: var(--tone-warning-fg);
}
.who {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 4px;
  flex-wrap: wrap;
}
.uname {
  font-weight: 550;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 110px;
}
.who .btn {
  margin-left: auto;
  height: 26px;
  padding: 0 8px;
  font-size: 12px;
}
.live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-3);
}
.live .ldot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-3);
}
.live.on {
  color: var(--tone-success-fg);
}
.live.on .ldot {
  background: var(--tone-success-fg);
  box-shadow: 0 0 0 3px var(--tone-success-bg);
}
.empty-rail {
  padding: 8px;
  font-size: 12.5px;
  color: var(--text-3);
}
.main {
  min-width: 0;
  min-height: 0;
  overflow: auto;
  display: flex;
  flex-direction: column;
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
.scrim {
  display: none;
}

@media (max-width: 760px) {
  .app {
    grid-template-columns: minmax(0, 1fr);
    height: auto;
    min-height: 100vh;
  }
  .rail {
    display: none;
    position: fixed;
    inset: 0 auto 0 0;
    width: min(280px, 88vw);
    box-shadow: var(--shadow-hover);
  }
  .app.rail-open .rail {
    display: flex;
  }
  .app.rail-open .scrim {
    display: block;
    position: fixed;
    inset: 0;
    background: rgb(0 0 0 / 35%);
    z-index: 15;
  }
  .mobilebar {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 16px;
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
    width: 34px;
    height: 34px;
    cursor: pointer;
  }
  .brand-m {
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
