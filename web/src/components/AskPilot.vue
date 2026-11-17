<script setup lang="ts">
import type { MilestoneView } from '../types'
import TaskChat from './TaskChat.vue'

// AskPilot is the Desk chat with a milestone's pilot. Its open proposals,
// with Accept and Reject, are on the Needs you tab (D530-P2).
defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()
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
  />
  <div v-else class="card empty">
    No pilot for this milestone. Autopilot, or <code>/pilot coordinate</code> on an issue, starts one.
  </div>
</template>
