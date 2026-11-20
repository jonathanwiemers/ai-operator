<script setup lang="ts">
// The rail's content (docs/contributing/web-ui.md, "Navigation"): Inbox, Overview, Metrics, Plans, projects,
// research, engine meters and the user. App.vue renders it in the aside on
// tablet and desktop and in the rail sheet on phones; App.vue loads the data.
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { hasFeature, session } from '../api'
import { logout } from '../client'
import { aboutLine } from '../about'
import { loginLasts, windowLabel } from '../format'
import { live } from '../live'
import { milestoneCounts, projectDot, projectNeedsYou, projectStateLine } from '../stream'
import type { EngineUsage, ProjectSummary, ResearchSummary } from '../types'
import type { ProjectSetSummary } from '../generated/views'
import { groupProjects, setCountsLine, setProblem } from '../projectSets'
import Truncated from '../ui/Truncated.vue'
import RelTime from './RelTime.vue'
import ThemeSwitch from './ThemeSwitch.vue'

const props = defineProps<{
  projects: ProjectSummary[] | null
  // null when not loaded or the request failed: the flat list as before.
  projectSets?: ProjectSetSummary[] | null
  researches: ResearchSummary[] | null
  inboxCount: number
}>()

// navigate: a link was tapped. The sheet closes on it, also for a link to
// the page it is on, which the router does not navigate to again.
const emit = defineEmits<{ navigate: [] }>()

const route = useRoute()
const about = computed(() => (session.about ? aboutLine(session.about) : null))
const lasts = computed(() => (session.me?.authMode === 'oidc' ? loginLasts(session.me, Date.now()) : null))

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

// The project rows: explicit projects first, then each set's header and its
// children (D558-P1); always expanded (D558-P3).
type ProjectRow =
  | { kind: 'project'; p: ProjectSummary; child: boolean }
  | { kind: 'set'; set: ProjectSetSummary; counts: string; problem: string | null }
const projectRows = computed<ProjectRow[]>(() => {
  const g = groupProjects(props.projects ?? [], props.projectSets ?? [])
  const rows: ProjectRow[] = g.ungrouped.map((p) => ({ kind: 'project', p, child: false }))
  for (const { set, children } of g.sets) {
    rows.push({ kind: 'set', set, counts: setCountsLine(set.counts), problem: setProblem(set) })
    for (const p of children) rows.push({ kind: 'project', p, child: true })
  }
  return rows
})
const rowKey = (r: ProjectRow) => (r.kind === 'set' ? `set:${r.set.namespace}/${r.set.name}` : `${r.p.namespace}/${r.p.name}`)

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
      <!-- Metrics read the history database: the feature history says it is there. -->
      <RouterLink v-if="hasFeature('history')" to="/metrics" class="item" active-class="active" data-rail-metrics>
        <span class="dot" />
        <span class="name">Metrics<span class="sub">cost, code, flow and pauses, across projects</span></span>
      </RouterLink>
      <RouterLink v-if="hasFeature('plans')" to="/plans" class="item" active-class="active" data-rail-plans>
        <span class="dot" />
        <span class="name">Plans<span class="sub">milestone plans, across projects</span></span>
      </RouterLink>

      <div class="sect"><span>Projects</span></div>
      <template v-for="r in projectRows" :key="rowKey(r)">
        <!-- A set's header: name, counts and last discovery; warning tone
             with the reason as title when Ready or Discovered is False. -->
        <div
          v-if="r.kind === 'set'"
          class="sethead"
          :class="{ warn: r.problem }"
          :title="r.problem ?? undefined"
          data-rail-set
        >
          <span class="setname">{{ r.set.name }}</span>
          <span class="setmeta">
            <span data-set-counts>{{ r.counts }}</span>
            <span v-if="r.set.lastDiscoveryTime"> · discovered <RelTime :time="r.set.lastDiscoveryTime" /></span>
            <span v-else> · not discovered yet</span>
          </span>
          <span v-if="r.problem" class="sr-only">{{ r.problem }}</span>
        </div>
        <RouterLink
          v-else
          :to="{ name: 'stream-project', params: { namespace: r.p.namespace, name: r.p.name } }"
          class="item"
          :class="{ active: projectActive(r.p.namespace, r.p.name), child: r.child }"
          active-class=""
        >
          <span
            class="dot"
            :class="{
              run: projectDot(r.p.stages) === 'running',
              warn: projectDot(r.p.stages) === 'needs-you',
              ok: projectDot(r.p.stages) === 'idle',
            }"
          />
          <span class="name">
            {{ r.p.name }}
            <span
              class="sub"
              :title="milestoneCounts(r.p.activeMilestones, r.p.settlingMilestones) ? 'settling: done, waiting up to 7 days for retrospective follow-ups' : undefined"
              >{{ [projectStateLine(r.p.stages, r.p.autopilots), milestoneCounts(r.p.activeMilestones, r.p.settlingMilestones)].filter(Boolean).join(' · ') }}</span
            >
          </span>
          <span v-if="projectNeedsYou(r.p.stages)" class="count">{{ projectNeedsYou(r.p.stages) }}</span>
        </RouterLink>
      </template>
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
      <ThemeSwitch />
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
.item.child {
  margin-left: 12px;
}
/* A set's header: the counts wrap under the name when the rail is narrow. */
.sethead {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  column-gap: 8px;
  padding: 10px 8px 4px;
  font-size: 12.5px;
  color: var(--text-2);
}
.sethead .setname {
  font-weight: 600;
  min-width: 0;
  overflow-wrap: anywhere;
}
.sethead .setmeta {
  font-size: 11.5px;
  color: var(--text-3);
}
.sethead.warn,
.sethead.warn .setmeta {
  color: var(--tone-warning-fg);
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
  /* Counts take the danger tone, never the primary colour of the buttons. */
  background: var(--tone-danger-bg);
  color: var(--tone-danger-fg);
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
