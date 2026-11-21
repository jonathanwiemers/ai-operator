<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { alertColor } from '../badgeTone'
import { getTimeline } from '../client'
import { useNow, useResource } from '../live'
import { legendTitle, overviewGroups, overviewLayout, overviewWindow, overviewWindows, windowRange } from '../timeline'
import Timeline from '../ui/Timeline.vue'

// The Overview's Timeline across the viewer's projects (R11, Decision 4):
// a 6 h / 24 h / 7 d pill switch in the section's toolbar, kept in
// ?window= (24 h by default), the milestone legend, and one row per
// project. It loads with the Overview in its own chunk (D667-P1) and
// reloads on task and project updates; between them the window slides
// along with useNow, so it does not fetch every minute.
const route = useRoute()
const router = useRouter()
const now = useNow()
const win = computed(() => overviewWindow(route.query.window))
const items = overviewWindows.map((w) => ({ value: w.value, label: w.label }))

const { data, error, loading } = useResource(() => {
  const w = win.value
  return () => getTimeline(windowRange(w, Date.now()))
}, ['task', 'project'])

const layout = computed(() => (data.value ? overviewLayout(data.value, now.value) : null))
const groups = computed(() => (layout.value ? overviewGroups(layout.value.projects) : []))
const legend = computed(() => (layout.value?.legend ?? []).map((e) => ({ key: e.key, title: legendTitle(e), token: e.token })))

function setWindow(v: string | number) {
  if (!overviewWindows.some((w) => w.value === v)) return
  void router.replace({ query: { ...route.query, window: String(v) } })
}
</script>

<template>
  <div data-overview-timeline>
    <UDashboardToolbar class="mb-2 min-h-0 border-0 px-0 sm:px-0" :ui="{ left: 'min-w-0' }">
      <template #left>
        <UTabs
          :model-value="win"
          :items="items"
          variant="pill"
          size="sm"
          :content="false"
          aria-label="Window"
          data-timeline-window
          @update:model-value="setWindow"
        />
      </template>
    </UDashboardToolbar>
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" :description="error" />
    <div v-else-if="loading && !data" class="empty">Loading…</div>
    <UEmpty
      v-else-if="!groups.length"
      icon="i-lucide-chart-gantt"
      title="No issue ran in this window."
      variant="naked"
      size="sm"
      data-timeline-empty
    />
    <template v-else-if="layout">
      <Timeline :groups="groups" :ticks="layout.ticks" :now-pct="layout.nowPct" :legend="legend" />
      <p v-if="data?.truncated" class="muted mt-1.5 text-[12px]" data-timeline-truncated>
        More issues ran in this window than the timeline shows.
      </p>
    </template>
  </div>
</template>
