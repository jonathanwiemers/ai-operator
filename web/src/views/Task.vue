<script setup lang="ts">
import { computed, watchEffect } from 'vue'
import { setCrumbNames } from '../crumbNames'
import { useResource } from '../live'
import TaskBody from '../components/TaskBody.vue'
import NotFound from './NotFound.vue'
import type { TaskResponse } from '../types'

const props = defineProps<{ namespace: string; name: string; task: string }>()
const { data, error, notFound, loading } = useResource<TaskResponse>(
  () =>
    `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/tasks/${encodeURIComponent(props.task)}`,
)

const t = computed(() => data.value?.task)

// The breadcrumbs put the task under its issue (R19).
watchEffect(() => {
  const v = t.value
  if (!v || v.name !== props.task) return
  const at = { namespace: props.namespace, name: props.name }
  setCrumbNames({
    task: { ...at, task: v.name, milestone: v.milestone?.iid, issue: v.issue?.iid },
    milestone: v.milestone && { ...at, iid: v.milestone.iid, title: v.milestone.title },
    issue: v.issue && { ...at, iid: v.issue.iid, title: v.issue.title },
  })
})

const heading = computed(() => {
  const v = t.value
  if (!v) return ''
  if (v.issue) return v.issue.title || `Issue #${v.issue.iid}`
  return `Plan: ${v.milestone?.title ?? v.name}`
})
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !t" class="card empty">Loading…</div>

    <template v-if="data && t">
      <div class="row">
        <span v-if="t.issue" class="iid">#{{ t.issue.iid }}</span>
        <h1>{{ heading }}</h1>
      </div>
      <TaskBody :namespace="namespace" :name="name" :task="task" :data="data" />
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
</style>
