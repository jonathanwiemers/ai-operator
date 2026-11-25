<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Badge from '../components/Badge.vue'
import InboxList from '../components/InboxList.vue'
import RelTime from '../components/RelTime.vue'
import { duration, plural } from '../format'
import { handledLabel, handledRoute, handledSubject, handledView, type HandledView } from '../inboxHandled'
import { pausedStrip } from '../inboxStrip'
import { getInbox } from '../client'
import { useResource } from '../live'
import { pageHeaderUi, toolbarUi } from '../pageHeader'
import { notify, notifyActive, openNotifyDialog, refreshPermission } from '../notify'

const { data, error, loading } = useResource(() => getInbox, ['project', 'task'])

// The Open | Handled switch lives in ?view=handled; no parameter is Open.
// A local switch (decision 8): UTabs pills; a pick is a new history
// entry, as the links were.
const route = useRoute()
const router = useRouter()
const view = computed(() => handledView(route.query.view))
const views: { value: HandledView; label: string }[] = [
  { value: 'open', label: 'Open' },
  { value: 'handled', label: 'Handled' },
]
function setView(v: string | number) {
  void router.push({ query: handledView(v) === 'handled' ? { view: 'handled' } : {} })
}
const handled = computed(() => data.value?.handled ?? [])

const strip = computed(() => pausedStrip(data.value?.items ?? [], Date.now()))

onMounted(refreshPermission)

// A handled row is a list item whose title link covers it (after:absolute),
// so a tap anywhere opens the item; the open items are InboxList's rows.
const handledRow =
  'relative grid min-h-11 gap-1 px-4 py-3 hover:bg-(--surface-2) tablet:grid-cols-[90px_minmax(120px,0.5fr)_1fr_auto] tablet:items-baseline tablet:gap-3'
const cover = 'text-(--text) after:absolute after:inset-0 hover:no-underline'
</script>

<template>
  <div class="page">
    <UPageHeader title="Inbox" description="What needs you now, across projects." :ui="pageHeaderUi">
      <template #links>
        <!-- Opens the Notifications dialog, which says when they are blocked. -->
        <UButton
          v-if="notify.permission !== 'unsupported'"
          :color="notifyActive() ? 'success' : 'neutral'"
          :variant="notifyActive() ? 'soft' : 'outline'"
          :icon="notifyActive() ? 'i-lucide-bell-ring' : 'i-lucide-bell'"
          :label="notifyActive() ? 'Notifications on' : 'Notify me'"
          aria-haspopup="dialog"
          data-inbox-notify
          @click="openNotifyDialog"
        />
      </template>
    </UPageHeader>
    <!-- The page's toolbar (decision 3): Open / Handled (?view=). -->
    <UDashboardToolbar :ui="toolbarUi" data-tab-toolbar>
      <template #left>
        <UTabs
          :model-value="view"
          :items="views"
          variant="pill"
          size="sm"
          :content="false"
          aria-label="Inbox view"
          data-inbox-view
          @update:model-value="setView"
        />
      </template>
    </UDashboardToolbar>
    <UAlert v-if="strip.count" :color="alertColor('warning')" variant="subtle" class="mt-3">
      <template #title>
        {{ strip.count }} {{ plural(strip.count, 'autopilot') }} paused<template v-if="strip.longestMs"
          >, longest {{ duration(strip.longestMs) }}</template
        >
      </template>
      <template #description>
      <div class="flex flex-wrap gap-x-3.5">
        <RouterLink
          v-for="m in strip.milestones"
          :key="`${m.namespace}/${m.project}/${m.milestoneIID}`"
          class="inline-flex max-w-full min-h-11 items-center break-words tablet:min-h-0"
          :to="{ name: 'stream-milestone', params: { namespace: m.namespace, name: m.project, iid: String(m.milestoneIID) } }"
        >
          {{ m.project }} · {{ m.title || `%${m.milestoneIID}` }}<template v-if="m.sinceMs != null"> ({{ duration(m.sinceMs) }})</template>
        </RouterLink>
      </div>
      </template>
    </UAlert>
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <div v-if="loading && !data" class="card empty">Loading…</div>
    <UEmpty v-else-if="data && view === 'open' && !data.items.length" icon="i-lucide-inbox" title="Nothing waiting." class="mt-[18px]" />
    <InboxList v-else-if="data && view === 'open'" class="mt-[18px]" :items="data.items" />
    <template v-else-if="data">
      <UEmpty v-if="!handled.length" icon="i-lucide-inbox" title="Nothing handled in the last 7 days." class="mt-[18px]" />
      <ul
        v-else
        class="m-0 mt-[18px] list-none divide-y divide-(--border) overflow-hidden rounded-(--radius) border border-(--border) bg-(--surface) p-0"
        data-handled
      >
        <li v-for="(item, i) in handled" :key="i" :class="handledRow" :data-handled-kind="item.kind">
          <span class="faint text-xs"><RelTime :time="item.at" /></span>
          <span class="font-semibold break-words"
            >{{ item.project }}<template v-if="item.milestoneIID"> · %{{ item.milestoneIID }}</template></span
          >
          <span class="min-w-0 break-words">
            <RouterLink :to="handledRoute(item)" :class="cover"
              ><b>{{ handledSubject(item) }}</b> {{ item.title }}</RouterLink
            >
          </span>
          <span class="min-w-0"><Badge :tone="handledLabel(item).tone" class="h-auto min-h-[22px] py-0.5 whitespace-normal">{{ handledLabel(item).text }}</Badge></span>
        </li>
      </ul>
    </template>
  </div>
</template>
