<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref } from 'vue'
import MilestoneList from './MilestoneList.vue'
import { getMilestones } from '../client'
import { useResource } from '../live'
import { sortInProgress } from '../milestoneRows'

// The milestone lists of the dashboard (across projects) and of a project
// page (namespace and name given): In progress, then Recently done, the
// newest 10 until Show all loads up to 200 (the API's cap). Done
// milestones need the research database: without it the section says so.
// Both reload on project and task changes. doneOnly leaves out In progress
// and its fetch (the Overview's Projects tab, whose cards say what is in work).
const props = defineProps<{ namespace?: string; name?: string; doneOnly?: boolean }>()

const RECENT = 10
const ALL = 200

const perProject = computed(() => !!props.namespace && !!props.name)
const project = computed(() => (perProject.value ? `${props.namespace}/${props.name}` : undefined))
const showAll = ref(false)

// The sources read project and showAll outside the closures, so a change
// loads again.
const open = useResource(() => {
  const p = project.value
  if (props.doneOnly) return null
  return () => getMilestones({ state: 'open', project: p })
}, ['project', 'task'])
const done = useResource(() => {
  const opts = { state: 'done' as const, limit: showAll.value ? ALL : RECENT, project: project.value }
  return () => getMilestones(opts)
}, ['project', 'task'])

const inProgress = computed(() => sortInProgress(open.data.value?.milestones ?? []))
const doneRows = computed(() => done.data.value?.milestones ?? [])
const history = computed(() => !!done.data.value?.history)
const more = computed(() => !showAll.value && doneRows.value.length >= RECENT)
</script>

<template>
  <section v-if="!doneOnly" class="section">
    <h2 class="text-[15px]">In progress</h2>
    <UAlert v-if="open.error.value" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="open.error.value" />
    <p v-if="open.loading.value && !open.data.value" class="muted">Loading…</p>
    <UEmpty v-else-if="open.data.value && !inProgress.length" icon="i-lucide-flag" title="No milestone is in work." size="sm" />
    <MilestoneList v-else-if="inProgress.length" :rows="inProgress" :show-project="!perProject" label="In progress" />
  </section>
  <section class="section">
    <h2 class="text-[15px]">Recently done</h2>
    <UAlert v-if="done.error.value" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="done.error.value" />
    <p v-if="done.loading.value && !done.data.value" class="muted">Loading…</p>
    <p v-else-if="done.data.value && !history" class="muted">Done milestones need the research database.</p>
    <UEmpty v-else-if="done.data.value && !doneRows.length" icon="i-lucide-flag" title="No milestone is done yet." size="sm" />
    <template v-else-if="doneRows.length">
      <MilestoneList :rows="doneRows" :show-project="!perProject" label="Recently done" />
      <div v-if="more" class="mt-2.5 text-center">
        <UButton color="neutral" variant="outline" label="Show all" @click="showAll = true" />
      </div>
    </template>
  </section>
</template>
