<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import Badge from '../components/Badge.vue'
import { alertColor } from '../badgeTone'
import { computed, watchEffect } from 'vue'
import { setCrumbNames } from '../crumbNames'
import { costTooltip } from '../format'
import { getMilestoneIssue, getTask } from '../client'
import { useResource } from '../live'
import { issueStateTone, issueTime, rowCost, rowTokens, tierLabel } from '../milestoneRows'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import HeaderActions from '../components/HeaderActions.vue'
import IssueStream from '../components/IssueStream.vue'
import StatTile from '../components/StatTile.vue'
import { toneText } from '../badgeTone'
import { pageHeaderUi } from '../pageHeader'
import TaskBody from '../components/TaskBody.vue'
import NotFound from './NotFound.vue'

// The issue page (R10, decision 9): the issue's figures as its row on the
// milestone's Issues tab, then, with a live task, what the Task page holds,
// and the issue's stream. It reads the snapshot and history once the
// tasks are gone. Titles are untrusted text and render as text.
const props = defineProps<{ namespace: string; name: string; iid: string; issue: string }>()

// The props are read outside the closures, so another issue loads again.
const { data, error, notFound, loading } = useResource(() => {
  const { namespace, name, iid, issue } = props
  return () => getMilestoneIssue(namespace, name, iid, issue)
}, ['project', 'task'])
const v = computed(() => data.value)

// The live task, loaded as the Task page loads it; none without one.
const taskName = computed(() => v.value?.task ?? '')
const { data: taskData } = useResource(() => {
  const { namespace, name } = props
  const task = taskName.value
  return task ? () => getTask(namespace, name, task) : null
})
const task = computed(() => (taskData.value?.task.name === taskName.value ? taskData.value : null))

watchEffect(() => {
  const x = v.value
  if (!x || x.namespace !== props.namespace || x.project !== props.name) return
  const at = { namespace: x.namespace, name: x.project }
  setCrumbNames({
    milestone: { ...at, iid: x.milestoneIID, title: x.milestoneTitle },
    issue: { ...at, iid: x.iid, title: x.title },
  })
})

// gone says why there is no live transcript: never started, or the task
// (and its pod) is gone.
const gone = computed(() => {
  const x = v.value
  if (!x || x.task) return ''
  if (x.stored) return 'From history: the tasks of this milestone are gone, and the live transcript went with their pods.'
  if (x.state === 'planned' || x.state === 'pending') return 'No task works on this issue yet.'
  return 'The task of this issue is gone, and its live transcript went with its pod.'
})

// The header's actions: the Task page, and the issue and its merge request
// on GitLab, which the task's header below holds with a live task.
const actions = computed(() => {
  const x = v.value
  if (!x) return []
  const { namespace, name } = props
  return [
    ...(!task.value && x.mergeRequestIID ? [{ label: `Merge request !${x.mergeRequestIID}`, href: x.mergeRequestURL }] : []),
    ...(x.task ? [{ label: 'Task page', to: { name: 'task' as const, params: { namespace, name, task: x.task } } }] : []),
    ...(task.value ? [] : [{ label: 'Issue', href: x.webURL }]),
  ]
})
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <div v-if="loading && !v" class="card empty">Loading…</div>

    <template v-if="v">
      <!-- The state is the eyebrow (decision 4). -->
      <UPageHeader :ui="pageHeaderUi">
        <template #headline>
          <span class="capitalize" :class="toneText[issueStateTone(v.state)]" data-issue-state>{{ v.state }}</span>
        </template>
        <template #title>
          <span class="me-2 font-mono font-normal text-(--text-3)">#{{ v.iid }}</span>{{ v.title || `Issue #${v.iid}` }}
        </template>
        <!-- The planner's tier and its reason (untrusted text). -->
        <template v-if="v.tier" #description>
          <span class="flex flex-wrap items-center gap-x-2 gap-y-1" data-issue-tier>
            <Badge class="tabular-nums" data-tier><span class="sr-only">Tier </span>{{ tierLabel(v.tier) }}</Badge>
            <span v-if="v.tier.why" class="[overflow-wrap:anywhere]">{{ v.tier.why }}</span>
          </span>
        </template>
        <template #links><HeaderActions :actions="actions" /></template>
      </UPageHeader>

      <div class="stat-tiles section" data-issue-figures>
        <StatTile label="Wall time" :value="issueTime(v, 'wallSec')" />
        <StatTile label="Agent time" :value="issueTime(v, 'agentSec')" />
        <StatTile label="Paused" :value="issueTime(v, 'pausedSec')" />
        <StatTile label="Cost">
          <span :title="v.figures ? costTooltip(v.figures) : undefined">{{ v.figures ? rowCost(v.figures) : '—' }}</span>
        </StatTile>
        <StatTile label="Tokens" :value="v.figures ? rowTokens(v.figures) : '—'" />
      </div>

      <p v-if="gone" class="note">{{ gone }}</p>

      <section v-if="v.task" class="section">
        <h2>Task</h2>
        <TaskBody v-if="task" :namespace="namespace" :name="name" :task="task.task.name" :data="task" />
        <div v-else class="card empty">Loading…</div>
      </section>

      <section class="section">
        <h2>Stream</h2>
        <IssueStream
          :namespace="namespace"
          :name="name"
          :milestone="v.milestoneIID"
          :issue="v.iid"
          :prompts="task?.prompts"
          :can-steer="!!task?.permissions?.steer"
        />
      </section>
    </template>
  </div>
</template>

<style scoped>
.note {
  margin: 14px 0 0;
  font-size: 13px;
  color: var(--text-3);
}
</style>
