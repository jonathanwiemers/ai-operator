<script setup lang="ts">
// The rail's content (docs/ui.md, "Navigation"): Inbox, Overview, projects,
// research, engine meters and the user. App.vue renders it in the aside on
// tablet and desktop and in the rail sheet on phones; App.vue loads the data.
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { hasFeature, session } from '../api'
import { logout } from '../client'
import { windowLabel } from '../format'
import { live } from '../live'
import { projectDot, projectNeedsYou, projectStateLine } from '../stream'
import type { EngineUsage, ProjectSummary, ResearchSummary } from '../types'
import Truncated from '../ui/Truncated.vue'

const props = defineProps<{
  projects: ProjectSummary[] | null
  researches: ResearchSummary[] | null
  inboxCount: number
}>()

// navigate: a link was tapped. The sheet closes on it, also for a link to
// the page it is on, which the router does not navigate to again.
const emit = defineEmits<{ navigate: [] }>()

const route = useRoute()

function onClick(e: MouseEvent) {
  if ((e.target as Element | null)?.closest('a')) emit('navigate')
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

function researchSub(r: ResearchSummary): string {
  if (r.phase === 'Running') return 'running'
  if (r.schedule) return r.schedule
  return r.oneOff ? 'one-off' : ''
}

function projectActive(namespace: string, name: string) {
  return route.params.namespace === namespace && route.params.name === name && route.path.startsWith('/projects/')
}
</script>

<template>
  <div class="railnav" @click="onClick">
    <nav>
      <RouterLink to="/inbox" class="item">
        <span class="dot" :class="{ warn: inboxCount > 0 }" />
        <span class="name">Inbox<span class="sub">needs you, across projects</span></span>
        <span v-if="inboxCount" class="count">{{ inboxCount }}</span>
      </RouterLink>
      <RouterLink to="/" class="item" active-class="active">
        <span class="dot" />
        <span class="name">Overview<span class="sub">what runs, across projects</span></span>
      </RouterLink>

      <div class="sect"><span>Projects</span></div>
      <RouterLink
        v-for="p in projects ?? []"
        :key="p.namespace + '/' + p.name"
        :to="{ name: 'stream-project', params: { namespace: p.namespace, name: p.name } }"
        class="item"
        :class="{ active: projectActive(p.namespace, p.name) }"
        active-class=""
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
        >
          <span class="dot" :class="{ run: r.phase === 'Running', ok: r.phase === 'Succeeded' }" />
          <span class="name">{{ r.name }}<span class="sub">{{ researchSub(r) }}</span></span>
        </RouterLink>
        <RouterLink to="/research" class="item soft">
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
        <!-- A clipped name opens on tap; the roles stay a hover hint, nothing depends on them. -->
        <Truncated class="max-w-[110px] font-[550]" :title="(session.me?.roles ?? []).join(', ') || 'no roles'">{{ session.me?.username }}</Truncated>
        <!-- Utilities, not UButton: the rail is in the initial bundle (main.ts, bundle budget). -->
        <button
          v-if="session.me?.authMode === 'oidc'"
          type="button"
          class="ms-auto inline-flex h-[26px] cursor-pointer items-center justify-center rounded-(--radius-sm) border border-(--border-strong) bg-(--surface) px-2 text-xs font-[550] text-(--text) hover:bg-(--surface-2)"
          @click="logout"
        >
          Sign out
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.railnav {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
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

/* Rail items are links styled as controls: 44 px on phones, like the
   buttons (style.css, "Hit areas"). */
@media (max-width: 759.98px) {
  .item {
    min-height: 44px;
  }
}
</style>
