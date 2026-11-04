<script setup lang="ts">
import { autopilotWaitingFor, cost } from '../format'
import { queueDone } from '../milestoneQueue'
import type { ProjectDetail } from '../types'
import Badge from './Badge.vue'
import Board from './Board.vue'
import ExtLink from './ExtLink.vue'
import PauseCardCompact from './PauseCardCompact.vue'
import RelTime from './RelTime.vue'
import StageBadge from './StageBadge.vue'

// ProjectBoards is a project's board: per milestone its autopilot, plan and
// issues, then the issues without a milestone.
defineProps<{ namespace: string; name: string; project: ProjectDetail }>()

function apTone(phase: string | undefined) {
  return phase === 'Paused' ? 'warning' : phase === 'Running' ? 'progress' : phase === 'Done' ? 'success' : 'muted'
}
</script>

<template>
  <div v-if="!project.milestones.length && !project.unplanned.length" class="card empty section">
    No milestones or issues are handed to the agents yet. Add <code>/agent spec</code> to a milestone's
    description, or move an issue to <code>agent::ready</code>.
  </div>

  <section v-for="m in project.milestones" :key="m.iid" class="section milestone">
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
      <RouterLink
        v-if="m.spec"
        :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(m.iid) } }"
      >
        {{ m.trackingIssueIID ? 'Stream' : 'Milestone stream' }}
      </RouterLink>
      <span class="faint">{{ m.tasks.length }} issues · {{ cost(m.costUSD) }}</span>
    </div>

    <PauseCardCompact
      v-if="m.spec?.status.autopilot?.phase === 'Paused'"
      class="pause"
      :kind="m.spec.status.autopilot.pauseKind"
      :reason="m.spec.status.autopilot.pausedReason"
      :issue="m.spec.status.autopilot.pausedFor"
      :waiting="autopilotWaitingFor(m.spec.status.autopilot, m.tasks)"
      :to="{ name: 'milestone', params: { namespace, name, iid: String(m.iid) } }"
    />

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
          {{ queueDone(m.spec.status.autopilot.items) }} / {{ m.spec.status.autopilot.items.length }} done ·
        </template>
        updated <RelTime :time="m.spec.status.run?.lastActivityTime ?? m.spec.createdAt" />
      </span>
    </RouterLink>

    <Board :tasks="m.tasks" />
  </section>

  <section v-if="project.unplanned.length" class="section milestone">
    <div class="row mhead">
      <h2>Issues without a milestone</h2>
    </div>
    <Board :tasks="project.unplanned" />
  </section>
</template>

<style scoped>
.milestone {
  margin-top: 36px;
}

.milestone:first-child {
  margin-top: 0;
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

.pause {
  margin-bottom: 12px;
}
</style>
