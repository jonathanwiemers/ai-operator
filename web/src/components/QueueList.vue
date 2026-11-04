<script setup lang="ts">
import { computed } from 'vue'
import { queueRows, queueTone } from '../milestoneQueue'
import type { MilestoneView } from '../types'
import Badge from './Badge.vue'
import ExtLink from './ExtLink.vue'

// QueueList is a milestone's autopilot queue: per issue its state, merge
// request, implement task and the decisions recorded for it. Titles and
// decision titles are untrusted text: rendered as text only.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()

const rows = computed(() =>
  queueRows(props.milestone.autopilot?.items ?? [], props.milestone.spec.status.autopilot?.items),
)
const issueURL = (iid: number) => props.milestone.webURL.replace(/\/-\/milestones\/\d+$/, `/-/issues/${iid}`)
</script>

<template>
  <ul class="queue">
    <li v-for="q in rows" :key="q.iid">
      <div class="top">
        <ExtLink :href="issueURL(q.iid)" class="iid">#{{ q.iid }}</ExtLink>
        <span class="tt" :title="q.title">{{ q.title }}</span>
        <Badge :tone="queueTone(q.state)">{{ q.state.toLowerCase() }}</Badge>
      </div>
      <div v-if="q.mergeRequestIID || q.task || q.decisions.length || q.revisitOf" class="sub">
        <ExtLink v-if="q.mergeRequestURL" :href="q.mergeRequestURL">!{{ q.mergeRequestIID }}</ExtLink>
        <span v-else-if="q.mergeRequestIID">!{{ q.mergeRequestIID }}</span>
        <RouterLink v-if="q.task" :to="{ name: 'task', params: { namespace, name, task: q.task } }">task</RouterLink>
        <span v-if="q.revisitOf">revisits {{ q.revisitOf }}</span>
        <span v-for="d in q.decisions" :key="d.id" class="decision" :class="{ superseded: d.superseded }" :title="d.title">
          <ExtLink v-if="d.url" :href="d.url"><code>{{ d.id }}</code></ExtLink>
          <code v-else>{{ d.id }}</code>
        </span>
      </div>
    </li>
  </ul>
  <div v-if="!rows.length" class="empty-sm">The queue is empty.</div>
</template>

<style scoped>
.queue {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 2px;
  font-size: 13px;
}
.queue li {
  padding: 4px 6px;
  border-radius: 6px;
}
.queue li:hover {
  background: var(--surface-2);
}
.top {
  display: grid;
  grid-template-columns: 38px minmax(0, 1fr) auto;
  gap: 6px;
  align-items: center;
}
.iid {
  font-family: var(--mono);
  font-size: 12px;
}
.tt {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sub {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin-left: 44px;
  font-size: 12px;
  color: var(--text-3);
}
.decision.superseded {
  opacity: 0.55;
  text-decoration: line-through;
}
.empty-sm {
  font-size: 13px;
  color: var(--text-3);
}
</style>
