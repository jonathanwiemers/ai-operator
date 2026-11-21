<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed } from 'vue'
import { alertColor } from '../badgeTone'
import { getTimeline } from '../client'
import { useNow, useResource } from '../live'
import { milestoneGroups, milestoneLayout } from '../timeline'
import Timeline from '../ui/Timeline.vue'

// A milestone's Timeline on its Issues tab (R10): GET /api/v1/timeline of
// the milestone, reloaded on task and project updates; the now line and
// running bars move with useNow. No forecast: with nothing run yet it says
// so, and Lanes shows what waits (D666-P2).
const props = defineProps<{ namespace: string; name: string; milestone: number }>()

const now = useNow()
const { data, error, loading } = useResource(() => {
  const { namespace, name, milestone } = props
  return () => getTimeline({ project: `${namespace}/${name}`, milestone })
}, ['task', 'project'])

const layout = computed(() => (data.value ? milestoneLayout(data.value, now.value) : null))
const groups = computed(() => (layout.value ? milestoneGroups(layout.value.groups) : []))
</script>

<template>
  <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" :description="error" />
  <UAlert
    v-else-if="data && !data.history"
    :color="alertColor('info')"
    variant="subtle"
    description="History needs the research database."
  />
  <div v-else-if="loading && !data" class="empty">Loading…</div>
  <UEmpty
    v-else-if="!groups.length"
    icon="i-lucide-chart-gantt"
    title="Nothing has run yet."
    variant="naked"
    size="sm"
    data-timeline-empty
  />
  <Timeline v-else-if="layout" :groups="groups" :ticks="layout.ticks" :now-pct="layout.nowPct" />
</template>
