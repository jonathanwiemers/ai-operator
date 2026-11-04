<script setup lang="ts">
import { cost } from '../format'
import type { MilestoneView } from '../types'
import Badge from './Badge.vue'
import RelTime from './RelTime.vue'

// PilotProposals is a milestone's pilot: its autonomy, cost and open
// proposals. Accept and Reject are commands in the pilot's chat (Ask the
// pilot). Proposal summaries are untrusted text: rendered as text only.
defineProps<{ namespace: string; name: string; pilot: NonNullable<MilestoneView['pilot']> }>()
</script>

<template>
  <div class="pilot">
    <div class="row head">
      <span class="muted">Autonomy</span>
      <Badge :tone="pilot.autonomy === 'managed' ? 'progress' : 'neutral'">{{ pilot.autonomy }}</Badge>
      <span class="spacer" />
      <span class="faint">{{ cost(pilot.costUSD) }}</span>
      <RouterLink :to="{ name: 'task', params: { namespace, name, task: pilot.task } }">Pilot task</RouterLink>
    </div>
    <div v-if="!pilot.proposals.length" class="faint proposals">No open proposals.</div>
    <div v-else class="proposals">
      <div v-for="p in pilot.proposals" :key="p.id" class="proposal">
        <div class="row">
          <code>P{{ p.id }}</code>
          <span class="faint"><RelTime :time="p.postedAt" /></span>
          <span class="spacer" />
          <span class="faint">accept or reject in the pilot's chat</span>
        </div>
        <div class="text">{{ p.summary }}</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.head {
  gap: 10px;
  flex-wrap: wrap;
}
.proposals {
  margin-top: 12px;
}
.proposal {
  padding: 10px 0;
  border-top: 1px solid var(--border);
}
.proposal .row {
  gap: 10px;
  margin-bottom: 4px;
  flex-wrap: wrap;
}
.text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
