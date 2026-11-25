<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { alertColor } from '../badgeTone'
import { getInbox, getOverview } from '../client'
import { overviewLines } from '../cockpitLive'
import ContextLayout from '../components/ContextLayout.vue'
import Cockpit from '../components/cockpit/Cockpit.vue'
import InboxList from '../components/InboxList.vue'
import MilestoneSections from '../components/MilestoneSections.vue'
import OverviewPanel from '../components/OverviewPanel.vue'
import { useNow, useResource } from '../live'
import { pageHeaderUi } from '../pageHeader'
import RouteTabs from '../ui/RouteTabs.vue'
import { dashboardAlias, dashboardTabFromRoute, dashboardTabs, tabQuery } from '../streamTabs'

// The dashboard at / (R16 of "Desk: the Cockpit"): Cockpit, the default,
// and Timeline (?tab=timeline). The Cockpit pins the Inbox's open items of
// all projects (InboxList, each a link to its card in the milestone
// Cockpit; nothing is answered here, D6), then the stream across projects
// (merges and milestone state shown, the rest folded per project), then the
// live lines of Working now; no composer (D9). The panel is Working now with
// the summary and spend tiles (OverviewPanel): a column from 1180 px, below
// a sheet that the header's Working now button opens. The Timeline tab is
// the Timeline across projects and the milestone lists. An old ?tab=activity,
// stream or overview opens the Cockpit (dashboardAlias, D791-P2).
const route = useRoute()
const router = useRouter()
const tab = computed(() => dashboardTabFromRoute(route.query.tab))
watch(
  () => route.query,
  (query) => {
    const next = dashboardAlias(query)
    if (next) void router.replace({ query: next, hash: route.hash })
  },
  { immediate: true },
)

// The Timeline loads in its own chunk, out of the initial bundle (D667-P1).
const OverviewTimeline = defineAsyncComponent(() => import('../components/OverviewTimeline.vue'))
const overview = useResource(() => getOverview, ['project', 'task'])
const inbox = useResource(() => getInbox, ['project', 'task'])
const now = useNow()
const lines = computed(() => overviewLines(overview.data.value?.working, now.value))

const panelOpen = ref(false)
const panelOpener = ref<HTMLElement | null>(null)
function openPanel(e: MouseEvent) {
  panelOpener.value = e.currentTarget as HTMLElement
  panelOpen.value = true
}
</script>

<template>
  <ContextLayout
    v-model:open="panelOpen"
    title="Working now"
    description="What runs now across projects, the engines' usage and the spend"
    :opener="panelOpener"
  >
    <template #default="{ sheet }">
      <!-- The header and the tab row, then the tab, which scrolls. -->
      <div class="flex min-h-0 min-w-0 flex-col">
        <div class="px-4 pt-3.5 tablet:px-7">
          <UPageHeader title="Overview" :ui="pageHeaderUi">
            <template v-if="sheet" #links>
              <UButton
                color="neutral"
                variant="outline"
                size="sm"
                class="min-h-11 justify-center tablet:min-h-0"
                label="Working now"
                aria-haspopup="dialog"
                :aria-expanded="panelOpen"
                data-panel-open="working"
                @click="openPanel"
              />
            </template>
          </UPageHeader>
          <RouteTabs :tabs="dashboardTabs" :active="tab" :to="(id) => ({ query: tabQuery(route.query, id, dashboardTabs) })" />
        </div>

        <div v-if="tab === 'timeline'" class="min-h-0 flex-1 overflow-y-auto px-4 pt-4 pb-10 tablet:px-7">
          <section class="section" aria-labelledby="overview-timeline-head">
            <h2 id="overview-timeline-head" class="text-[15px]">Timeline</h2>
            <!-- The load error shows here too: below 1180 px the panel's copy is in a closed sheet. -->
            <UAlert v-if="overview.error.value" :color="alertColor('danger')" variant="subtle" :description="overview.error.value" />
            <div v-if="overview.loading.value && !overview.data.value" class="empty">Loading…</div>
            <OverviewTimeline v-else-if="overview.data.value?.history" />
            <UAlert
              v-else-if="overview.data.value"
              :color="alertColor('info')"
              variant="subtle"
              description="Done milestones and spend need the research database."
              data-timeline-no-history
            />
          </section>
          <MilestoneSections />
        </div>

        <div v-else class="min-h-0 flex-1 overflow-y-auto px-4 pt-[18px] pb-10 tablet:px-7">
          <Cockpit :scope="{}">
            <template #pinned>
              <UAlert v-if="inbox.error.value" :color="alertColor('danger')" variant="subtle" :description="inbox.error.value" />
              <div v-if="inbox.loading.value && !inbox.data.value" class="empty">Loading…</div>
              <UEmpty
                v-else-if="inbox.data.value && !inbox.data.value.items.length"
                icon="i-lucide-inbox"
                title="Nothing needs you."
                variant="naked"
                size="sm"
              />
              <InboxList v-else-if="inbox.data.value" :items="inbox.data.value.items" />
            </template>
            <!-- The live lines of Working now (all projects), each a link to
                 its milestone's Cockpit or its task. -->
            <template #live>
              <section v-if="lines.length" aria-label="Running now" class="grid gap-1 border-t border-(--border) pt-2" data-cockpit-live>
                <UButton
                  v-for="l in lines"
                  :key="l.key"
                  color="neutral"
                  variant="ghost"
                  block
                  icon="i-lucide-activity"
                  class="justify-start text-left text-[13.5px]"
                  :to="l.to"
                  :label="l.text"
                  data-cockpit-live-line
                />
              </section>
            </template>
          </Cockpit>
        </div>
      </div>
    </template>
    <template #panel="{ sheet }">
      <OverviewPanel :overview="overview.data.value" :error="overview.error.value ?? undefined" :loading="overview.loading.value" :sheet="sheet" />
    </template>
  </ContextLayout>
</template>
