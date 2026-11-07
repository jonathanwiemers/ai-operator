<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import DashboardOverview from '../components/DashboardOverview.vue'
import { dashboardTabs, tabFromQuery, tabQuery } from '../streamTabs'
import Stream from './Stream.vue'

// The dashboard at / (R8, decisions 3 and 4): Overview, the default, and
// Stream (?tab=stream), the mixed stream of every project as before.
const route = useRoute()
const tab = computed(() => tabFromQuery(route.query.tab, dashboardTabs))
</script>

<template>
  <div class="dashboard">
    <div class="head">
      <h1>Overview</h1>
      <nav class="tabs" aria-label="View">
        <RouterLink
          v-for="t in dashboardTabs"
          :key="t.id"
          :to="{ query: tabQuery(route.query, t.id, dashboardTabs) }"
          :class="{ on: tab === t.id }"
          :aria-current="tab === t.id ? 'page' : undefined"
        >
          {{ t.label }}
        </RouterLink>
      </nav>
    </div>
    <Stream v-if="tab === 'stream'" class="pane" />
    <DashboardOverview v-else class="pane" />
  </div>
</template>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  min-height: 0;
  min-width: 0;
}
.head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 28px 0;
}
.head h1 {
  font-size: 20px;
  margin: 0;
  letter-spacing: -0.01em;
}
.tabs {
  display: flex;
  gap: 2px;
  padding: 2px;
  border-radius: var(--radius-sm);
  background: var(--surface-3);
}
.tabs a {
  padding: 3px 12px;
  border-radius: 5px;
  color: var(--text-2);
  font-size: 13px;
  font-weight: 500;
}
.tabs a:hover {
  text-decoration: none;
  color: var(--text);
}
.tabs a.on {
  background: var(--surface);
  color: var(--text);
  box-shadow: var(--shadow);
}
/* The Stream view sizes itself to the screen: here it fills what the tab
   bar leaves. */
.dashboard > .pane {
  flex: 1;
  min-height: 0;
}
@media (max-width: 760px) {
  .head {
    padding-left: 16px;
    padding-right: 16px;
  }
}
</style>
