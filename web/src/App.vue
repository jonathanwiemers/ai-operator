<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { get, isResearchPath, logout, session, setFeatures, LoginRequiredError } from './api'
import { connectEvents, disconnectEvents, live } from './live'
import SignIn from './views/SignIn.vue'
import type { Feature, Me } from './types'

// Who is logged in and which sections they see. get() sets loginRequired on
// 401; any other failure (the UI restarting) is retried, since the live
// updates and the route guard wait for it.
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

// Section tabs, only with more than one section: with the factory alone the
// bar looks as it always did.
const sections: Record<Feature['name'], { label: string; to: string }> = {
  factory: { label: 'Factory', to: '/' },
  research: { label: 'Research', to: '/research' },
}
const tabs = computed(() => {
  const known = (session.features ?? []).filter((f) => f.name in sections)
  return known.length > 1 ? known.map((f) => ({ name: f.name, ...sections[f.name] })) : []
})
const route = useRoute()
const activeSection = computed(() => (isResearchPath(route.path) ? 'research' : 'factory'))

onMounted(() => loadMe())

watch(
  () => session.me !== null && !session.loginRequired,
  (ready) => (ready ? connectEvents() : disconnectEvents()),
)
</script>

<template>
  <SignIn v-if="session.loginRequired" />
  <template v-else>
    <header class="topbar">
      <div class="inner">
        <RouterLink to="/" class="brand">
          <img src="/logo.svg" alt="" width="26" height="26" />
          <span>ai-operator</span>
        </RouterLink>
        <nav v-if="tabs.length" class="tabs">
          <RouterLink
            v-for="t in tabs"
            :key="t.name"
            :to="t.to"
            class="tab"
            :class="{ active: t.name === activeSection }"
            :aria-current="t.name === activeSection ? 'page' : undefined"
          >
            {{ t.label }}
          </RouterLink>
        </nav>
        <span class="spacer" />
        <span
          v-if="session.me"
          class="live"
          :class="{ on: live.connected }"
          :title="live.connected ? 'Updates arrive live' : 'Reconnecting…'"
        >
          <span class="dot" />{{ live.connected ? 'Live' : 'Offline' }}
        </span>
        <div v-if="session.me" class="user">
          <span class="name" :title="(session.me.roles ?? []).join(', ') || 'no roles'">{{ session.me.username }}</span>
          <button v-if="session.me.authMode === 'oidc'" class="btn" type="button" @click="logout">Sign out</button>
        </div>
      </div>
    </header>
    <main>
      <RouterView />
    </main>
  </template>
</template>

<style scoped>
.topbar {
  position: sticky;
  top: 0;
  z-index: 10;
  background: color-mix(in srgb, var(--surface) 88%, transparent);
  backdrop-filter: saturate(1.4) blur(8px);
  border-bottom: 1px solid var(--border);
}

.inner {
  max-width: 1400px;
  margin: 0 auto;
  height: 56px;
  padding: 0 24px;
  display: flex;
  align-items: center;
  gap: 16px;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
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

.spacer {
  flex: 1;
}

/* Full-height tabs: the active one is underlined in the accent, the line
   sitting on the bar's bottom border. A thin divider separates them from the
   brand. */
.tabs {
  align-self: stretch;
  display: flex;
  align-items: stretch;
  gap: 4px;
  padding-left: 16px;
  position: relative;
}

.tabs::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  height: 20px;
  margin-top: -10px;
  width: 1px;
  background: var(--border);
}

.tab {
  position: relative;
  display: flex;
  align-items: center;
  padding: 0 12px;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-2);
  transition: color 0.15s;
}

.tab::after {
  content: '';
  position: absolute;
  left: 8px;
  right: 8px;
  bottom: -1px;
  height: 2px;
  border-radius: 2px 2px 0 0;
  background: transparent;
  transition: background 0.15s;
}

.tab:hover {
  text-decoration: none;
  color: var(--text);
}

@media (hover: hover) {
  .tab:hover::after {
    background: var(--border-strong);
  }
}

.tab.active {
  color: var(--text);
}

.tab.active::after {
  background: var(--accent);
}

.tab:focus-visible {
  outline-offset: -6px;
  border-radius: var(--radius-sm);
}

.live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-3);
}

.live .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-3);
}

.live.on {
  color: var(--tone-success-fg);
}

.live.on .dot {
  background: var(--tone-success-fg);
  box-shadow: 0 0 0 3px var(--tone-success-bg);
}

.user {
  display: flex;
  align-items: center;
  gap: 12px;
}

.name {
  font-weight: 550;
  color: var(--text-2);
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 640px) {
  .inner {
    padding: 0 16px;
    gap: 12px;
  }
  .name {
    display: none;
  }
  /* With the tabs the bar gets tight: logo only, and the live dot without its label. */
  .brand span {
    display: none;
  }
  .tabs {
    padding-left: 12px;
    gap: 0;
  }
  .tab {
    padding: 0 8px;
  }
  .tab::after {
    left: 4px;
    right: 4px;
  }
  .live {
    font-size: 0;
  }
}
</style>
