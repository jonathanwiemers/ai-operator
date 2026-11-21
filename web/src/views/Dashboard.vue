<script setup lang="ts">
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import DashboardOverview from '../components/DashboardOverview.vue'
import { pageHeaderUi } from '../pageHeader'
import RouteTabs from '../ui/RouteTabs.vue'
import { dashboardTabs, tabFromQuery, tabQuery } from '../streamTabs'
import Stream from './Stream.vue'

// The dashboard at / (R8, decisions 3 and 4): Overview, the default, and
// Activity (?tab=activity; an old ?tab=stream opens it too), the mixed
// stream of every project as before.
const route = useRoute()
const tab = computed(() => tabFromQuery(route.query.tab, dashboardTabs))
</script>

<template>
  <!-- The Stream view (Activity) sizes itself to the screen: here it fills what the
       header and the tab row leave. -->
  <div class="flex min-h-0 min-w-0 flex-col">
    <div class="px-4 pt-3.5 tablet:px-7">
      <UPageHeader title="Overview" :ui="pageHeaderUi" />
      <RouteTabs
        :tabs="dashboardTabs"
        :active="tab"
        :to="(id) => ({ query: tabQuery(route.query, id, dashboardTabs) })"
      />
    </div>
    <Stream v-if="tab === 'activity'" class="min-h-0 flex-1" />
    <DashboardOverview v-else class="min-h-0 flex-1" />
  </div>
</template>
