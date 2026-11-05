<script setup lang="ts">
import { onMounted } from 'vue'
import { useResource } from '../live'
import { disableNotify, enableNotify, notify, notifyActive, refreshPermission } from '../notify'
import { inboxRoute } from '../inboxRoute'
import type { InboxResponse } from '../types'
import PauseCardCompact from '../components/PauseCardCompact.vue'
import RelTime from '../components/RelTime.vue'

const { data, error, loading } = useResource<InboxResponse>(() => '/inbox', ['project', 'task'])

onMounted(refreshPermission)
</script>

<template>
  <div class="page inbox">
    <div class="top">
      <h1>Inbox</h1>
      <span class="spacer" />
      <span v-if="notify.permission === 'denied'" class="faint notify-note">
        Notifications are blocked in this browser
      </span>
      <button
        v-else-if="notify.permission !== 'unsupported' && notifyActive()"
        type="button"
        class="btn notify on"
        title="Click to turn off"
        @click="disableNotify"
      >
        Notifications on
      </button>
      <button
        v-else-if="notify.permission !== 'unsupported'"
        type="button"
        class="btn notify"
        title="Get a browser notification for new pauses, proposals and questions while a Desk tab is open"
        @click="enableNotify"
      >
        Notify me
      </button>
    </div>
    <p class="muted">What needs you now, across projects.</p>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !data" class="card empty">Loading…</div>
    <div v-else-if="data && !data.items.length" class="card empty">Nothing waiting.</div>
    <ul v-else-if="data" class="list">
      <li v-for="(item, i) in data.items" :key="i">
        <PauseCardCompact
          v-if="item.kind === 'pause'"
          class="in-list"
          :kind="item.pauseKind"
          :reason="item.reason"
          :issue="item.issueIID"
          :project="item.project"
          :milestone-title="item.title"
          :to="inboxRoute(item)"
        />
        <RouterLink v-else :to="inboxRoute(item)" class="row">
          <span class="proj">{{ item.project }}</span>
          <span class="title">
            <b v-if="item.kind === 'proposal' && item.proposalID != null">P{{ item.proposalID }}</b>
            <b v-else-if="item.kind === 'pilotError'">Pilot write rejected</b>
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
.top {
  display: flex;
  align-items: center;
  gap: 8px 12px;
  flex-wrap: wrap;
}
.notify {
  height: 30px;
  padding: 0 12px;
  font-size: 13px;
}
.notify.on {
  color: var(--tone-success-fg);
  border-color: var(--tone-success-fg);
  background: var(--tone-success-bg);
}
.notify-note {
  font-size: 13px;
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
.list .in-list {
  border: 0;
  border-left: 3px solid var(--tone-warning-fg);
  border-radius: 0;
  box-shadow: none;
  padding: 12px 16px 12px 13px;
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
