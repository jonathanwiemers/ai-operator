<script setup lang="ts">
import { autopilotWaitingFor, costChip, costTooltip } from '../format'
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

// Utilities only (docs/ui.md, "Board"). A milestone's heading wraps; on
// phones its links are 44 px high. The columns are Board.vue's.
const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
const head = 'row mb-3'
const section = 'mt-9 min-w-0 first:mt-0'
</script>

<template>
  <div v-if="!project.milestones.length && !project.unplanned.length" class="card empty section">
    No milestones or issues are handed to the agents yet. Add <code>/agent spec</code> to a milestone's
    description, or move an issue to <code>agent::ready</code>.
  </div>

  <section v-for="m in project.milestones" :key="m.iid" :class="section" data-board-milestone>
    <div :class="head">
      <h2 class="min-w-0 [overflow-wrap:anywhere]">
        <ExtLink :href="m.webURL" :class="tap">{{ m.title }}</ExtLink>
      </h2>
      <span class="faint whitespace-nowrap">%{{ m.iid }}</span>
      <Badge v-if="m.spec?.status.autopilot && autopilotWaitingFor(m.spec.status.autopilot, m.tasks)" tone="progress">
        Autopilot waiting for #{{ autopilotWaitingFor(m.spec.status.autopilot, m.tasks) }}
      </Badge>
      <Badge v-else-if="m.spec?.status.autopilot" :tone="apTone(m.spec.status.autopilot.phase)">
        Autopilot {{ m.spec.status.autopilot.phase.toLowerCase() }}
      </Badge>
      <span class="spacer" />
      <RouterLink
        v-if="m.spec"
        :class="tap"
        :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(m.iid) } }"
      >
        {{ m.trackingIssueIID ? 'Stream' : 'Milestone stream' }}
      </RouterLink>
      <span class="faint" :title="costTooltip(m)">{{ m.tasks.length }} issues · {{ costChip(m) }}</span>
    </div>

    <PauseCardCompact
      v-if="m.spec?.status.autopilot?.phase === 'Paused'"
      class="mb-3"
      :kind="m.spec.status.autopilot.pauseKind"
      :reason="m.spec.status.autopilot.pausedReason"
      :issue="m.spec.status.autopilot.pausedFor"
      :waiting="autopilotWaitingFor(m.spec.status.autopilot, m.tasks)"
      :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(m.iid) }, query: { tab: 'pilot' } }"
    />

    <RouterLink
      v-if="m.spec"
      :to="{ name: 'task', params: { namespace: m.spec.namespace, name: m.spec.project, task: m.spec.name } }"
      class="mb-3 flex min-h-11 rounded-(--radius) border border-(--border) bg-(--surface) shadow-(--shadow) flex-wrap items-center gap-x-3 gap-y-1.5 px-3.5 py-2.5 text-inherit hover:border-(--border-strong) hover:no-underline hover:shadow-(--shadow-hover)"
      data-plan
    >
      <span class="font-[650]">Plan</span>
      <StageBadge :stage="m.spec.stage" />
      <span v-if="m.spec.status.run?.currentStep" class="muted min-w-0 [overflow-wrap:anywhere]">{{ m.spec.status.run.currentStep }}</span>
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

  <section v-if="project.unplanned.length" :class="section" data-board-milestone>
    <div :class="head">
      <h2>Issues without a milestone</h2>
    </div>
    <Board :tasks="project.unplanned" />
  </section>
</template>
