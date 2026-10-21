<script setup lang="ts">
import { computed } from 'vue'
import { session } from '../api'
import { useResource } from '../live'
import { cost } from '../format'
import Badge from '../components/Badge.vue'
import ExtLink from '../components/ExtLink.vue'
import RelTime from '../components/RelTime.vue'
import StageBar from '../components/StageBar.vue'
import type { ProjectSummary } from '../types'

const { data: projects, error, loading } = useResource<ProjectSummary[]>(() => '/projects')

const totals = computed(() => {
  const list = projects.value ?? []
  const sum = (f: (p: ProjectSummary) => number) => list.reduce((a, p) => a + f(p), 0)
  return {
    projects: list.length,
    working: sum((p) => p.stages.working ?? 0),
    attention: sum((p) => (p.stages['needs-input'] ?? 0) + (p.stages.failed ?? 0) + (p.stages.blocked ?? 0)),
    review: sum((p) => p.stages.review ?? 0),
    cost: sum((p) => Number.parseFloat(p.costUSD) || 0).toFixed(2),
  }
})

function unhealthy(p: ProjectSummary) {
  return (p.conditions ?? []).filter((c) => c.status === 'False')
}
</script>

<template>
  <div class="page">
    <div class="row head">
      <h1>Projects</h1>
    </div>

    <div v-if="error" class="banner danger">{{ error }}</div>

    <div v-if="projects && projects.length" class="facts totals">
      <div class="fact">
        <div class="label">Projects</div>
        <div class="value">{{ totals.projects }}</div>
      </div>
      <div class="fact">
        <div class="label">Agents working</div>
        <div class="value">{{ totals.working }}</div>
      </div>
      <div class="fact">
        <div class="label">Need attention</div>
        <div class="value">{{ totals.attention }}</div>
      </div>
      <div class="fact">
        <div class="label">In review</div>
        <div class="value">{{ totals.review }}</div>
      </div>
      <div class="fact">
        <div class="label">Model spend</div>
        <div class="value">{{ cost(totals.cost) }}</div>
      </div>
    </div>

    <div v-if="loading && !projects" class="card empty">Loading…</div>
    <div v-else-if="projects && projects.length === 0" class="card empty">
      <h2>No projects</h2>
      <p v-if="session.me?.authMode === 'oidc'">
        There are no AgentProjects, or none of your roles
        <template v-if="session.me.roles?.length">
          (<code>{{ session.me.roles.join(', ') }}</code>)
        </template>
        <template v-else>(you have none)</template>
        is granted access in the chart's <code>ui.authorization.rules</code>.
      </p>
      <p v-else>There are no AgentProjects in the cluster yet.</p>
    </div>

    <div v-else class="grid">
      <div v-for="p in projects" :key="p.namespace + '/' + p.name" class="project card">
        <div class="row">
          <div class="names">
            <RouterLink :to="{ name: 'project', params: { namespace: p.namespace, name: p.name } }" class="name">
              {{ p.name }}
            </RouterLink>
            <div class="ns faint">{{ p.namespace }}</div>
          </div>
          <span class="spacer" />
          <Badge v-if="p.suspend" tone="warning">Suspended</Badge>
          <Badge v-for="l in p.usageLimits" :key="l.engine" tone="warning" :title="l.message">
            {{ l.engine }} limit
          </Badge>
          <Badge v-for="c in unhealthy(p)" :key="c.type" tone="danger" :title="c.message">{{ c.reason || c.type }}</Badge>
        </div>
        <div class="gitlab muted">
          <ExtLink :href="p.gitlab.webURL">{{ p.gitlab.project }}</ExtLink>
        </div>
        <StageBar :stages="p.stages" />
        <div v-for="a in p.autopilots" :key="a.milestoneIID" class="autopilot">
          <Badge :tone="a.phase === 'Paused' ? 'warning' : a.phase === 'Running' ? 'progress' : 'muted'">
            Autopilot {{ a.phase.toLowerCase() }}
          </Badge>
          <span class="ap-title">{{ a.title }}</span>
          <span class="faint">{{ a.done }}/{{ a.total }}</span>
        </div>
        <div class="foot faint">
          <span>{{ p.tasks }} tasks</span>
          <span>{{ cost(p.costUSD) }}</span>
          <span>synced <RelTime :time="p.lastSyncTime" /></span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.head {
  margin-bottom: 18px;
}

.totals {
  margin-bottom: 20px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 16px;
}

.project {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 18px;
  color: inherit;
  transition:
    box-shadow 0.15s,
    border-color 0.15s;
}

.project:hover {
  border-color: var(--border-strong);
  box-shadow: var(--shadow-hover);
}

.names {
  min-width: 0;
}

.name {
  font-size: 16px;
  font-weight: 650;
  color: var(--text);
}

.name:hover {
  text-decoration: none;
}

/* The whole card opens the project; the GitLab link stays clickable. */
.name::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: var(--radius);
}

.gitlab :deep(a),
.project :deep([title]) {
  position: relative;
  z-index: 1;
}

.ns {
  font-size: 12px;
}

.gitlab {
  font-size: 13px;
  margin-top: -6px;
}

.autopilot {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.ap-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.foot {
  display: flex;
  gap: 14px;
  font-size: 12px;
  border-top: 1px solid var(--border);
  padding-top: 10px;
}

.empty p {
  max-width: 520px;
  margin: 8px auto 0;
}
</style>
