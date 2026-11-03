<script setup lang="ts">
import { computed } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { pauseEndsBy, pauseTitle } from '../pauseCard'
import type { PauseKind } from '../types'

// PauseCardCompact is the pause card's short form for the Inbox, the project
// board and the stream: why the autopilot paused, over which issue, what ends
// it, and a link to the milestone page, where the full card has the actions.
// The reason is untrusted text: rendered as text, on one line.
const props = defineProps<{
  kind?: PauseKind
  reason?: string
  issue?: number
  project?: string
  milestoneTitle?: string
  // waiting: the issue a run is working on now (the pause ends by itself).
  waiting?: number
  to: RouteLocationRaw
}>()

const endsBy = computed(() => pauseEndsBy(props.kind, props.issue || undefined))
const reasonText = computed(() => (props.reason || '').trim())
</script>

<template>
  <section class="card pause-compact">
    <div class="head">
      <strong class="title">{{ pauseTitle(kind) }}</strong>
      <span v-if="project || milestoneTitle || issue" class="where muted">
        <span v-if="project" class="proj">{{ project }}</span>
        <span v-if="milestoneTitle">{{ milestoneTitle }}</span>
        <span v-if="issue">#{{ issue }}</span>
      </span>
      <span class="spacer" />
      <RouterLink :to="to" class="open">Open</RouterLink>
    </div>
    <p v-if="reasonText" class="reason" :title="reasonText">{{ reasonText }}</p>
    <p v-else-if="!kind" class="reason muted">Waiting for a human.</p>
    <p v-if="waiting" class="waiting">A run is working on #{{ waiting }} now.</p>
    <p v-if="endsBy" class="faint ends">{{ endsBy }}</p>
  </section>
</template>

<style scoped>
.pause-compact {
  padding: 10px 14px;
  border-left: 3px solid var(--tone-warning-fg);
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.pause-compact p {
  margin: 0;
}

.head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}

.title {
  white-space: nowrap;
}

.where {
  display: flex;
  gap: 8px;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  font-size: 13px;
}

.where > span {
  overflow: hidden;
  text-overflow: ellipsis;
}

.where > span + span::before {
  content: '·';
  margin-right: 8px;
}

.proj {
  font-weight: 600;
}

.spacer {
  flex: 1;
}

.open {
  white-space: nowrap;
  font-size: 13px;
}

.reason,
.ends {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.waiting {
  color: var(--tone-info-fg);
  font-size: 13px;
}

.ends {
  font-size: 12.5px;
}
</style>
