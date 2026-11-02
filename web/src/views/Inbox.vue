<script setup lang="ts">
import { useResource } from '../live'
import type { InboxResponse } from '../types'
import RelTime from '../components/RelTime.vue'

const { data, error, loading } = useResource<InboxResponse>(() => '/inbox', ['project', 'task'])

function to(item: InboxResponse['items'][number]) {
  if (item.kind === 'task' && item.task) {
    return { name: 'task' as const, params: { namespace: item.namespace, name: item.project, task: item.task } }
  }
  if (item.milestoneIID) {
    return {
      name: 'stream-milestone' as const,
      params: { namespace: item.namespace, name: item.project, iid: String(item.milestoneIID) },
    }
  }
  return { name: 'stream-project' as const, params: { namespace: item.namespace, name: item.project } }
}
</script>

<template>
  <div class="page inbox">
    <h1>Inbox</h1>
    <p class="muted">What needs you now, across projects.</p>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !data" class="card empty">Loading…</div>
    <div v-else-if="data && !data.items.length" class="card empty">Nothing waiting.</div>
    <ul v-else-if="data" class="list">
      <li v-for="(item, i) in data.items" :key="i">
        <RouterLink :to="to(item)" class="row">
          <span class="proj">{{ item.project }}</span>
          <span class="title">
            <b v-if="item.kind === 'proposal' && item.proposalID != null">P{{ item.proposalID }}</b>
            <b v-else-if="item.issueIID">#{{ item.issueIID }}</b>
            {{ item.title }}
          </span>
          <span class="reason muted">{{ item.reason }}</span>
          <span class="when faint"><RelTime :time="item.at" /></span>
        </RouterLink>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.inbox h1 {
  margin-bottom: 4px;
}
.list {
  list-style: none;
  margin: 18px 0 0;
  padding: 0;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  overflow: hidden;
}
.list li + li {
  border-top: 1px solid var(--border);
}
.row {
  display: grid;
  grid-template-columns: 120px 1fr minmax(120px, 0.6fr) auto;
  gap: 12px;
  padding: 12px 16px;
  color: inherit;
  align-items: baseline;
}
.row:hover {
  background: var(--surface-2);
  text-decoration: none;
}
.proj {
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.reason {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.when {
  font-size: 12px;
}
@media (max-width: 720px) {
  .row {
    grid-template-columns: 1fr;
    gap: 4px;
  }
}
</style>
