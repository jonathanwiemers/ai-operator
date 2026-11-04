<script setup lang="ts">
import { computed } from 'vue'
import { useResource } from '../live'
import { cost } from '../format'
import { queueDone } from '../milestoneQueue'
import Badge from '../components/Badge.vue'
import Board from '../components/Board.vue'
import ExtLink from '../components/ExtLink.vue'
import RelTime from '../components/RelTime.vue'
import StageBadge from '../components/StageBadge.vue'
import Conversation from '../components/Conversation.vue'
import QueueList from '../components/QueueList.vue'
import PilotProposals from '../components/PilotProposals.vue'
import AskPilot from '../components/AskPilot.vue'
import PauseCard from '../components/PauseCard.vue'
import NotFound from './NotFound.vue'
import { hasFeature } from '../api'
import type { MilestoneView } from '../types'

// Milestone shows a planned milestone: the autopilot's queue, the pilot, the
// Desk chat with the pilot ("Ask the pilot") and the chat on the tracking
// issue. Pause reasons, titles, proposal summaries
// and notes are untrusted text: rendered as text only.
const props = defineProps<{ namespace: string; name: string; iid: string }>()
const { data: m, error, notFound, loading } = useResource<MilestoneView>(
  () =>
    `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/milestones/${encodeURIComponent(props.iid)}`,
)

const ap = computed(() => m.value?.autopilot)
const showPilotChat = computed(() => !!m.value?.permissions?.chat || hasFeature('chat'))
const done = computed(() => queueDone(ap.value?.items ?? []))
const apTone = (phase: string | undefined) =>
  phase === 'Paused' ? 'warning' : phase === 'Running' ? 'progress' : phase === 'Done' ? 'success' : 'muted'
// Comments likely changed when the queue or the pilot did.
const stamp = computed(() => {
  const v = m.value
  if (!v) return ''
  const items = (v.autopilot?.items ?? []).map((i) => `${i.iid}:${i.state}:${i.mergeRequestIID ?? ''}`).join(',')
  return `${v.autopilot?.phase ?? ''}|${items}|${(v.pilot?.proposals ?? []).map((p) => p.id).join(',')}|${v.spec.status.run?.observedRunRequest ?? ''}`
})
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <nav class="crumbs muted">
      <RouterLink :to="{ name: 'stream-project', params: { namespace, name } }">{{ name }}</RouterLink>
      <span>/</span>
      <RouterLink :to="{ name: 'project-board', params: { namespace, name } }">Board</RouterLink>
      <span>/</span>
      <span>%{{ iid }}</span>
    </nav>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !m" class="card empty">Loading…</div>

    <template v-if="m">
      <div class="head">
        <div class="row">
          <span class="iid">%{{ m.iid }}</span>
          <h1>{{ m.title }}</h1>
        </div>
        <div class="row links">
          <Badge v-if="ap" :tone="apTone(ap.phase)">Autopilot {{ ap.phase.toLowerCase() }}</Badge>
          <Badge v-else tone="muted">No autopilot</Badge>
          <span v-if="ap" class="faint">{{ done }} / {{ ap.items.length }} done</span>
          <span class="faint">{{ cost(m.costUSD) }}</span>
          <span class="spacer" />
          <RouterLink :to="{ name: 'task', params: { namespace, name, task: m.spec.name } }">Plan</RouterLink>
          <ExtLink :href="m.webURL">Milestone</ExtLink>
          <ExtLink v-if="m.tracking" :href="m.tracking.webURL">Tracking issue #{{ m.tracking.iid }}</ExtLink>
        </div>
      </div>

      <PauseCard v-if="ap?.phase === 'Paused'" :key="m.iid" :namespace="namespace" :name="name" :milestone="m" />

      <section v-if="ap" class="section">
        <h3>Queue</h3>
        <div class="card body">
          <QueueList :namespace="namespace" :name="name" :milestone="m" />
        </div>
      </section>

      <section class="section">
        <h3>Pilot</h3>
        <div v-if="m.pilot" class="card body">
          <PilotProposals :namespace="namespace" :name="name" :pilot="m.pilot" />
        </div>
        <div v-else class="card empty">This milestone has no pilot.</div>
      </section>

      <section v-if="showPilotChat" class="section">
        <h3>Ask the pilot</h3>
        <AskPilot :namespace="namespace" :name="name" :milestone="m" />
      </section>

      <section v-if="m.tracking && m.permissions?.gitlab" class="section">
        <h3>Tracking issue</h3>
        <Conversation :namespace="namespace" :name="name" :milestone="m.iid" :steer="!!m.permissions?.steer" :stamp="stamp" />
      </section>

      <section class="section">
        <div class="row section-head">
          <h3>Issues</h3>
          <StageBadge :stage="m.spec.stage" />
          <span class="faint">plan updated <RelTime :time="m.spec.status.run?.lastActivityTime ?? m.spec.createdAt" /></span>
        </div>
        <Board :tasks="m.tasks" />
      </section>
    </template>
  </div>
</template>

<style scoped>
.crumbs {
  display: flex;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 10px;
  flex-wrap: wrap;
}

.head h1 {
  overflow-wrap: anywhere;
}

.iid {
  font-family: var(--mono);
  font-size: 18px;
  color: var(--text-3);
}

.links {
  margin-top: 10px;
  gap: 14px;
  flex-wrap: wrap;
}

.banner {
  margin-top: 12px;
}

.section h3 {
  margin-bottom: 10px;
}

.section-head {
  gap: 10px;
  margin-bottom: 10px;
}

.section-head h3 {
  margin-bottom: 0;
}

.body {
  padding: 14px 16px;
}
</style>
