<script setup lang="ts">
import { computed } from 'vue'
import { useResource } from '../live'
import { cost } from '../format'
import Badge from '../components/Badge.vue'
import Board from '../components/Board.vue'
import ExtLink from '../components/ExtLink.vue'
import RelTime from '../components/RelTime.vue'
import StageBadge from '../components/StageBadge.vue'
import Conversation from '../components/Conversation.vue'
import TaskChat from '../components/TaskChat.vue'
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
const issueURL = (iid: number) => m.value?.webURL.replace(/\/-\/milestones\/\d+$/, `/-/issues/${iid}`)
const done = computed(() => (ap.value?.items ?? []).filter((i) => i.state === 'Merged' || i.state === 'Skipped').length)
const apTone = (phase: string | undefined) =>
  phase === 'Paused' ? 'warning' : phase === 'Running' ? 'progress' : phase === 'Done' ? 'success' : 'muted'
const itemTone = (s: string) => (s === 'Merged' ? 'success' : s === 'Active' ? 'progress' : s === 'Skipped' ? 'warning' : 'muted')
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

      <div v-if="ap?.waitingFor" class="banner info">
        <strong>Autopilot waiting</strong>
        <span>
          A run is working on #{{ ap.waitingFor }}; autopilot continues by itself once it is reviewed and CI is green.
          <template v-if="ap.pausedReason">It paused because: {{ ap.pausedReason }}</template>
        </span>
      </div>
      <div v-else-if="ap?.phase === 'Paused'" class="banner warning">
        <strong>Autopilot paused</strong>
        <span class="text">{{ ap.pausedReason || 'Waiting for a human.' }}</span>
      </div>

      <section v-if="ap" class="section">
        <h3>Queue</h3>
        <div class="card">
          <table class="list">
            <thead>
              <tr>
                <th>Issue</th>
                <th>State</th>
                <th>Merge request</th>
                <th>Task</th>
              </tr>
            </thead>
            <tbody>
              <tr v-if="!ap.items.length">
                <td colspan="4" class="faint">The queue is empty.</td>
              </tr>
              <tr v-for="it in ap.items" :key="it.iid">
                <td>
                  <ExtLink :href="issueURL(it.iid)">#{{ it.iid }}</ExtLink>
                  <span class="muted item-title">{{ it.title }}</span>
                </td>
                <td><Badge :tone="itemTone(it.state)">{{ it.state }}</Badge></td>
                <td>
                  <ExtLink v-if="it.mergeRequestURL" :href="it.mergeRequestURL">!{{ it.mergeRequestIID }}</ExtLink>
                  <span v-else-if="it.mergeRequestIID">!{{ it.mergeRequestIID }}</span>
                  <span v-else class="faint">—</span>
                </td>
                <td>
                  <RouterLink v-if="it.task" :to="{ name: 'task', params: { namespace, name, task: it.task } }" class="mono">
                    {{ it.task }}
                  </RouterLink>
                  <span v-else class="faint">—</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section class="section">
        <h3>Pilot</h3>
        <div v-if="m.pilot" class="card body">
          <div class="row pilot">
            <span class="muted">Autonomy</span>
            <Badge :tone="m.pilot.autonomy === 'managed' ? 'progress' : 'neutral'">{{ m.pilot.autonomy }}</Badge>
            <span class="spacer" />
            <span class="faint">{{ cost(m.pilot.costUSD) }}</span>
            <RouterLink :to="{ name: 'task', params: { namespace, name, task: m.pilot.task } }">Pilot task</RouterLink>
          </div>
          <div v-if="!m.pilot.proposals.length" class="faint proposals">No open proposals.</div>
          <div v-else class="proposals">
            <div v-for="p in m.pilot.proposals" :key="p.id" class="proposal">
              <div class="row">
                <code>P{{ p.id }}</code>
                <span class="faint"><RelTime :time="p.postedAt" /></span>
                <span class="spacer" />
                <span class="faint">answer <code>yes P{{ p.id }}</code> in the chat</span>
              </div>
              <div class="text">{{ p.summary }}</div>
            </div>
          </div>
        </div>
        <div v-else class="card empty">This milestone has no pilot.</div>
      </section>

      <section v-if="showPilotChat" class="section">
        <h3>Ask the pilot</h3>
        <TaskChat
          v-if="m.pilot"
          :key="m.pilot.task"
          :namespace="namespace"
          :name="name"
          :task="m.pilot.task"
          :can-post="!!m.permissions?.chat"
          :can-transcript="!!m.permissions?.transcript"
          :phase="m.pilot.phase"
          :message="m.pilot.message"
          :pod-name="m.pilot.podName"
          task-type="Coordinate"
          :proposals="m.pilot.proposals"
        />
        <div v-else class="card empty">
          No pilot for this milestone. Autopilot, or <code>/pilot coordinate</code> on an issue, starts one.
        </div>
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

.item-title {
  margin-left: 8px;
}

.pilot {
  gap: 10px;
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
}

.text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
