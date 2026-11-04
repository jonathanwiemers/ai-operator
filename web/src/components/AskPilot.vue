<script setup lang="ts">
import type { MilestoneView } from '../types'
import TaskChat from './TaskChat.vue'

// AskPilot is the Desk chat with a milestone's pilot, with Accept and Reject
// for its open proposals.
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
    :can-transcript="!!milestone.permissions?.transcript"
    :phase="milestone.pilot.phase"
    :message="milestone.pilot.message"
    :pod-name="milestone.pilot.podName"
    task-type="Coordinate"
    :proposals="milestone.pilot.proposals"
  />
  <div v-else class="card empty">
    No pilot for this milestone. Autopilot, or <code>/pilot coordinate</code> on an issue, starts one.
  </div>
</template>
