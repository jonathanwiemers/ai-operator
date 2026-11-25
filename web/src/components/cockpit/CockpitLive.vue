<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UCollapsible from '@nuxt/ui/components/Collapsible.vue'
import { computed } from 'vue'
import { liveLines, type LiveLine } from '../../cockpitLive'
import type { Task } from '../../generated/views'
import { useNow } from '../../live'
import LiveTranscript from '../LiveTranscript.vue'

// CockpitLive is the Cockpit's live lines (R11 of "Desk: the Cockpit"),
// in its live slot under the pinned cards: one line per issue whose implementer
// runs now, "#712 implementing · 4 min". The tasks come from the page's
// milestone, which live updates reload, so a line goes when its run ends.
// With the transcript grant a line opens the task's LiveTranscript inline
// (mounted only while open); without it, it links to the task page. On the
// project Cockpit (R15) milestones maps each task to its milestone, and a
// line links to that milestone's Cockpit.
const props = defineProps<{ namespace: string; name: string; tasks?: Task[]; transcript?: boolean; milestones?: Record<string, number> }>()

const now = useNow()
const lines = computed(() => liveLines(props.tasks, now.value))
function lineTo(l: LiveLine) {
  const { namespace, name } = props
  const iid = props.milestones?.[l.task]
  if (iid) return { name: 'stream-milestone' as const, params: { namespace, name, iid: String(iid) } }
  return { name: 'task' as const, params: { namespace, name, task: l.task } }
}
</script>

<template>
  <section v-if="lines.length" aria-label="Running now" class="cockpit-live" data-cockpit-live>
    <template v-for="l in lines" :key="l.task">
      <UCollapsible v-if="transcript && !milestones" data-cockpit-live-line>
        <template #default="{ open }">
          <UButton
            color="neutral"
            variant="ghost"
            block
            icon="i-lucide-activity"
            class="line"
            :trailing-icon="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
            :label="l.text"
          />
        </template>
        <template #content>
          <LiveTranscript class="transcript" :namespace="namespace" :name="name" :task="l.task" :pod="l.pod" :run="l.run" />
        </template>
      </UCollapsible>
      <UButton
        v-else
        color="neutral"
        variant="ghost"
        block
        icon="i-lucide-activity"
        class="line"
        :to="lineTo(l)"
        :label="l.text"
        data-cockpit-live-line
      />
    </template>
  </section>
</template>

<style scoped>
.cockpit-live {
  display: grid;
  gap: 4px;
  padding-top: 8px;
  border-top: 1px solid var(--border);
}
.line {
  justify-content: flex-start;
  font-size: 13.5px;
  text-align: left;
}
.line :deep(.iconify:last-child) {
  margin-left: auto;
}
.transcript {
  margin: 4px 0 8px;
}
</style>
