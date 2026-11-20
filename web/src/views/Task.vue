<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import { computed, watchEffect } from 'vue'
import { setCrumbNames } from '../crumbNames'
import { getTask } from '../client'
import { useResource } from '../live'
import TaskBody from '../components/TaskBody.vue'
import NotFound from './NotFound.vue'

// The Task page: TaskBody holds its header (UPageHeader, with the title)
// and everything below it.
const props = defineProps<{ namespace: string; name: string; task: string }>()
// The props are read outside the closure, so another task loads again.
const { data, error, notFound, loading } = useResource(() => {
  const { namespace, name, task } = props
  return () => getTask(namespace, name, task)
})

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
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <div v-if="loading && !t" class="card empty">Loading…</div>

    <TaskBody v-if="data && t" page :namespace="namespace" :name="name" :task="task" :data="data" />
  </div>
</template>
