<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import DashboardOverview from '../components/DashboardOverview.vue'
import RouteTabs from '../ui/RouteTabs.vue'
import { dashboardTabs, tabFromQuery, tabQuery } from '../streamTabs'
import Stream from './Stream.vue'

// The dashboard at / (R8, decisions 3 and 4): Overview, the default, and
// Stream (?tab=stream), the mixed stream of every project as before.
const route = useRoute()
const tab = computed(() => tabFromQuery(route.query.tab, dashboardTabs))
</script>

<template>
  <!-- The Stream view sizes itself to the screen: here it fills what the
       tab bar leaves. -->
  <div class="flex min-h-0 min-w-0 flex-col">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 pt-3.5 tablet:px-7">
      <h1 class="m-0 text-xl tracking-[-0.01em]">Overview</h1>
      <RouteTabs
        :tabs="dashboardTabs"
        :active="tab"
        :to="(id) => ({ query: tabQuery(route.query, id, dashboardTabs) })"
      />
    </div>
    <Stream v-if="tab === 'stream'" class="min-h-0 flex-1" />
    <DashboardOverview v-else class="min-h-0 flex-1" />
  </div>
</template>
