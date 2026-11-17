<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import Badge from '../components/Badge.vue'
import ExtLink from '../components/ExtLink.vue'
import Markdown from '../components/Markdown.vue'
import PauseCardCompact from '../components/PauseCardCompact.vue'
import RelTime from '../components/RelTime.vue'
import { duration } from '../format'
import { handledLabel, handledRoute, handledSubject, handledView, type HandledView } from '../inboxHandled'
import { inboxRoute } from '../inboxRoute'
import { pausedStrip } from '../inboxStrip'
import { getInbox } from '../client'
import { useResource } from '../live'
import { disableNotify, enableNotify, notify, notifyActive, refreshPermission } from '../notify'
import RouteTabs from '../ui/RouteTabs.vue'
import Truncated from '../ui/Truncated.vue'
import type { Tab } from '../streamTabs'

const { data, error, loading } = useResource(() => getInbox, ['project', 'task'])

// The Open | Handled switch lives in ?view=handled; no parameter is Open.
const route = useRoute()
const view = computed(() => handledView(route.query.view))
const views: Tab<HandledView>[] = [
  { id: 'open', label: 'Open' },
  { id: 'handled', label: 'Handled' },
]
const viewTo = (id: HandledView) => ({ query: id === 'handled' ? { view: 'handled' } : {} })
const handled = computed(() => data.value?.handled ?? [])

const strip = computed(() => pausedStrip(data.value?.items ?? [], Date.now()))

onMounted(refreshPermission)

// A row is a list item whose title link covers it (after:absolute), so a tap
// anywhere opens the item. What sits above the cover keeps its own tap: the
// companion's merge request link and a clipped reason, which opens in place.
const row =
  'relative grid min-h-11 gap-1 px-4 py-3 hover:bg-(--surface-2) tablet:grid-cols-[120px_1fr_minmax(120px,0.6fr)_auto] tablet:items-baseline tablet:gap-3'
const handledRow =
  'relative grid min-h-11 gap-1 px-4 py-3 hover:bg-(--surface-2) tablet:grid-cols-[90px_minmax(120px,0.5fr)_1fr_auto] tablet:items-baseline tablet:gap-3'
const cover = 'text-(--text) after:absolute after:inset-0 hover:no-underline'
const reason = 'muted min-w-0 text-[13px] [&[role=button]]:relative [&[role=button]]:z-1'
</script>

<template>
  <div class="page">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
      <h1 class="mb-1">Inbox</h1>
      <span class="flex-1" />
      <span v-if="notify.permission === 'denied'" class="faint text-[13px]">Notifications are blocked in this browser</span>
      <UButton
        v-else-if="notify.permission !== 'unsupported' && notifyActive()"
        color="success"
        variant="soft"
        icon="i-lucide-bell-ring"
        label="Notifications on"
        aria-pressed="true"
        @click="disableNotify"
      />
      <UButton
        v-else-if="notify.permission !== 'unsupported'"
        color="neutral"
        variant="outline"
        icon="i-lucide-bell"
        label="Notify me"
        aria-pressed="false"
        @click="enableNotify"
      />
    </div>
    <p class="muted">What needs you now, across projects.</p>
    <RouteTabs class="mt-2 w-fit" :tabs="views" :active="view" :to="viewTo" label="Inbox view" />
    <div v-if="strip.count" class="banner warning">
      <span
        ><b>{{ strip.count }} {{ strip.count === 1 ? 'autopilot' : 'autopilots' }} paused</b
        ><template v-if="strip.longestMs">, longest {{ duration(strip.longestMs) }}</template></span
      >
      <div class="flex flex-wrap gap-x-3.5">
        <RouterLink
          v-for="m in strip.milestones"
          :key="`${m.namespace}/${m.project}/${m.milestoneIID}`"
          class="inline-flex max-w-full min-h-11 items-center break-words tablet:min-h-0"
          :to="{ name: 'stream-milestone', params: { namespace: m.namespace, name: m.project, iid: String(m.milestoneIID) }, query: { tab: 'needs' } }"
        >
          {{ m.project }} · {{ m.title || `%${m.milestoneIID}` }}<template v-if="m.sinceMs != null"> ({{ duration(m.sinceMs) }})</template>
        </RouterLink>
      </div>
    </div>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !data" class="card empty">Loading…</div>
    <div v-else-if="data && view === 'open' && !data.items.length" class="card empty">Nothing waiting.</div>
    <ul
      v-else-if="data && view === 'open'"
      class="m-0 mt-[18px] list-none divide-y divide-(--border) overflow-hidden rounded-(--radius) border border-(--border) bg-(--surface) p-0"
    >
      <li v-for="(item, i) in data.items" :key="i">
        <PauseCardCompact
          v-if="item.kind === 'pause'"
          flat
          :kind="item.pauseKind"
          :reason="item.reason"
          :issue="item.issueIID"
          :hold="item.hold"
          :project="item.project"
          :milestone-title="item.title"
          :to="inboxRoute(item)"
        />
        <!-- A ready companion: the row opens the home task, the ref the companion MR in GitLab. -->
        <div v-else-if="item.kind === 'companionReady'" :class="row">
          <span class="font-semibold break-words">{{ item.project }}</span>
          <span class="min-w-0 break-words">
            <RouterLink :to="inboxRoute(item)" :class="cover"
              ><b>Companion ready</b><template v-if="item.issueIID"> · #{{ item.issueIID }}</template></RouterLink
            >
            {{ ' ' }}<ExtLink :href="item.url" class="mono relative z-1">{{ item.title }}</ExtLink>
          </span>
          <Truncated :lines="2" :class="reason"><Markdown mode="inline" :text="item.reason" /></Truncated>
          <span class="faint text-xs"><RelTime :time="item.at" /></span>
        </div>
        <div v-else :class="row">
          <span class="font-semibold break-words">{{ item.project }}</span>
          <span class="min-w-0 break-words">
            <RouterLink :to="inboxRoute(item)" :class="cover">
              <b v-if="item.kind === 'proposal' && item.proposalID != null">P{{ item.proposalID }}</b>
              <b v-else-if="item.kind === 'pilotError'">Pilot write rejected</b>
              <b v-else-if="item.issueIID">#{{ item.issueIID }}</b>
              {{ item.title }}
            </RouterLink>
          </span>
          <Truncated :lines="2" :class="reason"><Markdown mode="inline" :text="item.reason" /></Truncated>
          <span class="faint text-xs"><RelTime :time="item.at" /></span>
        </div>
      </li>
    </ul>
    <template v-else-if="data">
      <div v-if="!handled.length" class="card empty">Nothing handled in the last 7 days.</div>
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
