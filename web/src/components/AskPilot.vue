<script setup lang="ts">
import UEmpty from '@nuxt/ui/components/Empty.vue'
import type { MilestoneView } from '../types'
import TaskChat from './TaskChat.vue'

// AskPilot is the Desk chat with a milestone's pilot. Its open proposals,
// with Accept and Reject, are on the Needs you tab (D530-P2). A problem card
// opens it at its problem (focus, messagePrefix: TaskChat).
defineProps<{ namespace: string; name: string; milestone: MilestoneView; focus?: string; messagePrefix?: string }>()
</script>

<template>
  <TaskChat
    v-if="milestone.pilot"
    :key="milestone.pilot.task"
    :namespace="namespace"
    :name="name"
    :task="milestone.pilot.task"
    :can-post="!!milestone.permissions?.chat"
    :transcript="
      milestone.permissions?.transcript
        ? { name: 'task', params: { namespace, name, task: milestone.pilot.task }, query: { tab: 'live' } }
        : undefined
    "
    :phase="milestone.pilot.phase"
    :message="milestone.pilot.message"
    :pod-name="milestone.pilot.podName"
    task-type="Coordinate"
    :focus="focus"
    :message-prefix="messagePrefix"
  />
  <UEmpty v-else icon="i-lucide-bot" title="No pilot for this milestone.">
    <template #description>Autopilot, or <code>/pilot coordinate</code> on an issue, starts one.</template>
  </UEmpty>
</template>
