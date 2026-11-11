<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, onMounted } from 'vue'
import ExtLink from '../components/ExtLink.vue'
import Markdown from '../components/Markdown.vue'
import PauseCardCompact from '../components/PauseCardCompact.vue'
import RelTime from '../components/RelTime.vue'
import { duration } from '../format'
import { inboxRoute } from '../inboxRoute'
import { pausedStrip } from '../inboxStrip'
import { getInbox } from '../client'
import { useResource } from '../live'
import { disableNotify, enableNotify, notify, notifyActive, refreshPermission } from '../notify'
import Truncated from '../ui/Truncated.vue'

const { data, error, loading } = useResource(() => getInbox, ['project', 'task'])

const strip = computed(() => pausedStrip(data.value?.items ?? [], Date.now()))

onMounted(refreshPermission)

// A row is a list item whose title link covers it (after:absolute), so a tap
// anywhere opens the item. What sits above the cover keeps its own tap: the
// companion's merge request link and a clipped reason, which opens in place.
const row =
  'relative grid min-h-11 gap-1 px-4 py-3 hover:bg-(--surface-2) tablet:grid-cols-[120px_1fr_minmax(120px,0.6fr)_auto] tablet:items-baseline tablet:gap-3'
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
          :to="{ name: 'stream-milestone', params: { namespace: m.namespace, name: m.project, iid: String(m.milestoneIID) }, query: { tab: 'pilot' } }"
        >
          {{ m.project }} · {{ m.title || `%${m.milestoneIID}` }}<template v-if="m.sinceMs != null"> ({{ duration(m.sinceMs) }})</template>
        </RouterLink>
      </div>
    </div>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !data" class="card empty">Loading…</div>
    <div v-else-if="data && !data.items.length" class="card empty">Nothing waiting.</div>
    <ul
      v-else-if="data"
      class="m-0 mt-[18px] list-none divide-y divide-(--border) overflow-hidden rounded-(--radius) border border-(--border) bg-(--surface) p-0"
    >
      <li v-for="(item, i) in data.items" :key="i">
        <PauseCardCompact
          v-if="item.kind === 'pause'"
          flat
          :kind="item.pauseKind"
          :reason="item.reason"
          :issue="item.issueIID"
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
  </div>
</template>
