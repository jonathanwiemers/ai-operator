<script setup lang="ts">
import { computed } from 'vue'
import { useResource } from '../live'
import { cost, autopilotWaitingFor } from '../format'
import Badge from '../components/Badge.vue'
import Board from '../components/Board.vue'
import ExtLink from '../components/ExtLink.vue'
import RelTime from '../components/RelTime.vue'
import StageBadge from '../components/StageBadge.vue'
import StageBar from '../components/StageBar.vue'
import NotFound from './NotFound.vue'
import type { ProjectDetail } from '../types'

const props = defineProps<{ namespace: string; name: string }>()
const { data: p, error, notFound, loading } = useResource<ProjectDetail>(
  () => `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}`,
)

const unhealthy = computed(() => (p.value?.conditions ?? []).filter((c) => c.status === 'False' && c.reason !== 'Suspended'))

function apTone(phase: string | undefined) {
  return phase === 'Paused' ? 'warning' : phase === 'Running' ? 'progress' : phase === 'Done' ? 'success' : 'muted'
}
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <nav class="crumbs muted">
      <RouterLink to="/">Projects</RouterLink>
      <span>/</span>
      <span>{{ namespace }}</span>
    </nav>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !p" class="card empty">Loading…</div>

    <template v-if="p">
      <div class="row title">
        <h1>{{ p.name }}</h1>
        <Badge v-if="p.suspend" tone="warning">Suspended</Badge>
        <span class="spacer" />
        <ExtLink :href="p.gitlab.webURL" class="gl">{{ p.gitlab.project }}</ExtLink>
      </div>

      <div v-for="l in p.usageLimits" :key="l.engine" class="banner warning">
        <strong>{{ l.engine }}</strong>
        <span>hit its usage limit. Runs that need it wait; the limit resets <RelTime :time="l.until" />.</span>
        <span v-if="l.message" class="faint">{{ l.message }}</span>
      </div>
      <div v-for="c in unhealthy" :key="c.type" class="banner danger">
        <strong>{{ c.reason || c.type }}</strong>
        <span>{{ c.message }}</span>
      </div>
      <div v-if="p.suspend" class="banner info">
        The project is suspended: GitLab is not polled and no new runs start.
      </div>

      <div class="facts summary">
        <div class="fact">
          <div class="label">Tasks</div>
          <div class="value">{{ p.tasks }}</div>
        </div>
        <div class="fact">
          <div class="label">Parallel runs</div>
          <div class="value">up to {{ p.maxConcurrentRuns }}</div>
        </div>
        <div class="fact">
          <div class="label">Last sync</div>
          <div class="value"><RelTime :time="p.lastSyncTime" /></div>
        </div>
        <div class="fact">
          <div class="label">Poll interval</div>
          <div class="value">{{ p.pollInterval }}</div>
        </div>
        <div class="fact">
          <div class="label">Model spend</div>
          <div class="value">{{ cost(p.costUSD) }}</div>
        </div>
      </div>
      <div class="card stages">
        <StageBar :stages="p.stages" />
      </div>

      <div v-if="!p.milestones.length && !p.unplanned.length" class="card empty section">
        No milestones or issues are handed to the agents yet. Add <code>/agent spec</code> to a milestone's
        description, or move an issue to <code>agent::ready</code>.
      </div>

      <section v-for="m in p.milestones" :key="m.iid" class="section milestone">
        <div class="row mhead">
          <h2>
            <ExtLink :href="m.webURL">{{ m.title }}</ExtLink>
          </h2>
          <span class="faint">%{{ m.iid }}</span>
          <Badge v-if="m.spec?.status.autopilot && autopilotWaitingFor(m.spec.status.autopilot, m.tasks)" tone="progress">
            Autopilot waiting for #{{ autopilotWaitingFor(m.spec.status.autopilot, m.tasks) }}
          </Badge>
          <Badge v-else-if="m.spec?.status.autopilot" :tone="apTone(m.spec.status.autopilot.phase)">
            Autopilot {{ m.spec.status.autopilot.phase.toLowerCase() }}
          </Badge>
          <span class="spacer" />
          <RouterLink v-if="m.spec" :to="{ name: 'milestone', params: { namespace, name, iid: m.iid } }">
            {{ m.trackingIssueIID ? 'Queue and chat' : 'Milestone page' }}
          </RouterLink>
          <span class="faint">{{ m.tasks.length }} issues · {{ cost(m.costUSD) }}</span>
        </div>

        <RouterLink
          v-if="m.spec"
          :to="{ name: 'task', params: { namespace: m.spec.namespace, name: m.spec.project, task: m.spec.name } }"
          class="spec card"
        >
          <span class="spec-label">Plan</span>
          <StageBadge :stage="m.spec.stage" />
          <span v-if="m.spec.status.run?.currentStep" class="muted">{{ m.spec.status.run.currentStep }}</span>
          <span v-else-if="m.spec.status.plan" class="muted">{{ m.spec.status.plan.length }} planned issues</span>
          <span class="spacer" />
          <span class="faint">
            <template v-if="m.spec.status.autopilot?.items">
              {{ m.spec.status.autopilot.items.filter((i) => i.state === 'Merged' || i.state === 'Skipped').length }}
              /
              {{ m.spec.status.autopilot.items.length }} done ·
            </template>
            updated <RelTime :time="m.spec.status.run?.lastActivityTime ?? m.spec.createdAt" />
          </span>
        </RouterLink>

        <div v-if="m.spec?.status.autopilot && autopilotWaitingFor(m.spec.status.autopilot, m.tasks)" class="banner info">
          <strong>Autopilot waiting</strong>
          <span>
            A run is working on #{{ m.spec.status.autopilot.pausedFor }}; autopilot continues by itself once it is reviewed and
            CI is green. It paused because {{ m.spec.status.autopilot.pausedReason }}.
          </span>
        </div>
        <div v-else-if="m.spec?.status.autopilot?.phase === 'Paused'" class="banner warning">
          <strong>Autopilot paused</strong>
          <span>
            {{ m.spec.status.autopilot.pausedReason || 'Waiting for a human.' }}
            <template v-if="m.spec.status.autopilot.resumeWhenReady">
              It continues by itself once a later run of #{{ m.spec.status.autopilot.pausedFor }} fixes this.
            </template>
          </span>
        </div>

        <Board :tasks="m.tasks" />
      </section>

      <section v-if="p.unplanned.length" class="section milestone">
        <div class="row mhead">
          <h2>Issues without a milestone</h2>
        </div>
        <Board :tasks="p.unplanned" />
      </section>
    </template>
  </div>
</template>

<style scoped>
.crumbs {
  display: flex;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 10px;
}

.title {
  gap: 12px;
}

.gl {
  font-size: 13px;
}

.summary {
  margin-top: 18px;
}

.stages {
  margin-top: 12px;
  padding: 14px 16px;
}

.milestone {
  margin-top: 36px;
}

.mhead {
  margin-bottom: 12px;
}

.spec {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 14px;
  margin-bottom: 12px;
  color: inherit;
  flex-wrap: wrap;
}

.spec:hover {
  text-decoration: none;
  border-color: var(--border-strong);
  box-shadow: var(--shadow-hover);
}

.spec-label {
  font-weight: 650;
}

.banner {
  margin-bottom: 12px;
}
</style>
