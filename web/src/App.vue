<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { get, logout, session } from './api'
import { connectEvents, disconnectEvents, live } from './live'
import SignIn from './views/SignIn.vue'
import type { Me } from './types'

onMounted(async () => {
  try {
    session.me = await get<Me>('/me')
  } catch {
    // get() sets loginRequired on 401; other errors show up in the views.
  }
})

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
  }
  .name {
    display: none;
  }
}
</style>
