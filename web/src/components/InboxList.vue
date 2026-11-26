<script setup lang="ts">
import { inboxRoute } from '../inboxRoute'
import type { InboxItem } from '../types'
import Truncated from '../ui/Truncated.vue'
import ExtLink from './ExtLink.vue'
import Markdown from './Markdown.vue'
import PauseCardCompact from './PauseCardCompact.vue'
import RelTime from './RelTime.vue'

// InboxList is the Inbox's open items, one row each, as the Inbox page and
// the Overview Cockpit's pinned cards (R16 of "Desk: the Cockpit") show
// them. Each row links to the item's card (inboxRoute); nothing is answered
// here (D6). A row is a list item whose title link covers it
// (after:absolute), so a tap anywhere opens the item. What sits above the
// cover keeps its own tap: the companion's merge request link and a clipped
// reason, which opens in place.
defineProps<{ items: InboxItem[] }>()

const row =
  'relative grid min-h-11 gap-1 px-4 py-3 hover:bg-(--surface-2) tablet:grid-cols-[120px_1fr_minmax(120px,0.6fr)_auto] tablet:items-baseline tablet:gap-3'
const cover = 'text-(--text) after:absolute after:inset-0 hover:no-underline'
const reason = 'muted min-w-0 text-[13px] [&[role=button]]:relative [&[role=button]]:z-1'
</script>

<template>
  <ul class="m-0 list-none divide-y divide-(--border) overflow-hidden rounded-(--radius) border border-(--border) bg-(--surface) p-0">
    <li v-for="(item, i) in items" :key="i" :data-inbox-kind="item.kind">
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
      <!-- A ready plan: the reason is the start of the agent's reply, untrusted, shown as plain text. -->
      <div v-else-if="item.kind === 'plan'" :class="row">
        <span class="font-semibold break-words">{{ item.project }}</span>
        <span class="min-w-0 break-words">
          <RouterLink :to="inboxRoute(item)" :class="cover"><b>Plan ready</b> · {{ item.title || 'Untitled plan' }}</RouterLink>
        </span>
        <Truncated :lines="2" :class="reason" :text="item.reason" />
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
</template>
