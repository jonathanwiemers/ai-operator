<script setup lang="ts">
import { computed, watchEffect } from 'vue'
import { setCrumbNames } from '../crumbNames'
import { costTooltip } from '../format'
import { getMilestoneIssue, getTask } from '../client'
import { useResource } from '../live'
import { issueStateTone, issueTime, rowCost, rowTokens } from '../milestoneRows'
import Badge from '../components/Badge.vue'
import ExtLink from '../components/ExtLink.vue'
import IssueStream from '../components/IssueStream.vue'
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
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !v" class="card empty">Loading…</div>

    <template v-if="v">
      <div class="row">
        <span class="iid">#{{ v.iid }}</span>
        <h1>{{ v.title || `Issue #${v.iid}` }}</h1>
      </div>
      <div class="row links">
        <Badge :tone="issueStateTone(v.state)">{{ v.state }}</Badge>
        <span class="spacer" />
        <RouterLink v-if="v.task" :to="{ name: 'task', params: { namespace, name, task: v.task } }">
          Task page
        </RouterLink>
        <!-- With a live task, the task's row below holds these links. -->
        <template v-if="!task">
          <ExtLink :href="v.webURL">Issue</ExtLink>
          <ExtLink v-if="v.mergeRequestIID" :href="v.mergeRequestURL">Merge request !{{ v.mergeRequestIID }}</ExtLink>
        </template>
      </div>

      <div class="facts section">
        <div class="fact">
          <div class="label">Wall time</div>
          <div class="value">{{ issueTime(v, 'wallSec') }}</div>
        </div>
        <div class="fact">
          <div class="label">Agent time</div>
          <div class="value">{{ issueTime(v, 'agentSec') }}</div>
        </div>
        <div class="fact">
          <div class="label">Paused</div>
          <div class="value">{{ issueTime(v, 'pausedSec') }}</div>
        </div>
        <div class="fact">
          <div class="label">Cost</div>
          <div class="value" :title="v.figures ? costTooltip(v.figures) : undefined">
            {{ v.figures ? rowCost(v.figures) : '—' }}
          </div>
        </div>
        <div class="fact">
          <div class="label">Tokens</div>
          <div class="value">{{ v.figures ? rowTokens(v.figures) : '—' }}</div>
        </div>
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
          :can-steer="!!task?.permissions?.steer"
        />
      </section>
    </template>
  </div>
</template>

<style scoped>
h1 {
  overflow-wrap: anywhere;
}

.iid {
  font-family: var(--mono);
  font-size: 18px;
  color: var(--text-3);
}

.links {
  margin-top: 10px;
  gap: 14px;
}

.note {
  margin: 14px 0 0;
  font-size: 13px;
  color: var(--text-3);
}
</style>
