<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import CompanionsList from './CompanionsList.vue'
import ExtLink from './ExtLink.vue'
import QueueList from './QueueList.vue'
import RelTime from './RelTime.vue'
import { costTooltip } from '../format'
import { queueDone, waitingForTurn } from '../milestoneQueue'
import { issueProgress, issueStateTone, issueTime, progressPct, rowCost, rowTokens, stateTone } from '../milestoneRows'
import {
  interleaveReleases,
  releaseBy,
  releaseDone,
  releaseHeadline,
  releaseMuted,
  releaseNote,
  releaseStateLabel,
  releaseTime,
  releaseTone,
} from '../releaseRows'
import type { MilestoneIssueRow, MilestoneView } from '../types'

// A milestone's Issues tab (R9, decision 8): its state, progress and cost,
// then one row per issue with its state, merge request, wall, agent and
// paused time, cost and tokens, and the version it shipped in. Releases are
// rows between the issues (R12, decision 15). The page header holds the
// title. Titles, a release's why and reason are untrusted text and render
// as text.
//
// The table is one grid (docs/ui.md, "Milestone Issues"): from desktop width
// the column heads and every row are subgrids of it, and a release row spans
// all columns. Below it (the rail leaves too little room for eight columns on
// a tablet) the heads are hidden; an issue row shows its number
// and title on the first line, then its state, merge request and labelled
// figures as a wrapping line. Links in rows are 44 px high on phones.
const props = defineProps<{ milestone: MilestoneView; namespace: string; name: string }>()

const rows = computed(() => interleaveReleases(props.milestone.issues ?? [], props.milestone.releases))
const progress = computed(() => issueProgress(props.milestone.issues ?? []))
const pct = computed(() => progressPct({ progress: progress.value }))
const waiting = computed(() => waitingForTurn(props.milestone.autopilot?.queuedBehind))

// Every issue links to its issue page, also once its task is gone.
function issueTo(i: MilestoneIssueRow) {
  const { namespace, name } = props
  return { name: 'issue' as const, params: { namespace, name, iid: String(props.milestone.iid), issue: String(i.iid) } }
}

// num: a figure, tabular and on one line, right-aligned in the table.
const num = 'whitespace-nowrap tabular-nums text-(--text-2) desktop:text-right'
// tap: a link in a row, at least 44 px high on phones.
const tap = 'min-h-11 tablet:min-h-0'
// The edge tracks are 14 px wider than their content: a subgrid's padding
// (px-3.5) is taken from them.
const columns = 'desktop:grid-cols-[minmax(0,1fr)_90px_64px_64px_64px_64px_72px_78px]'
const sectionHead =
  'm-0 mb-2 flex justify-between text-[11.5px] font-semibold tracking-[0.06em] text-(--text-3) uppercase'
</script>

<template>
  <section>
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-[13px]">
      <Badge :tone="stateTone(milestone.state)">{{ milestone.state }}</Badge>
      <span class="flex min-w-[200px] items-center gap-2" :title="`${pct}% merged`">
        <span
          class="h-1.5 flex-1 overflow-hidden rounded-full bg-(--surface-3)"
          role="progressbar"
          :aria-valuenow="pct"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-label="`${pct}% merged`"
        >
          <span class="block h-full bg-(--tone-success-fg)" :style="{ width: `${pct}%` }" />
        </span>
        <span class="whitespace-nowrap tabular-nums text-(--text-2)">{{ progress.merged }}/{{ progress.total }} merged</span>
      </span>
      <span class="whitespace-nowrap tabular-nums text-(--text-2)" :title="costTooltip(milestone)">{{ rowCost(milestone) }}</span>
      <span class="whitespace-nowrap tabular-nums text-(--text-2)">{{ rowTokens(milestone) }} tok</span>
    </div>
    <p v-if="milestone.stored" class="mt-2.5 mb-0 text-[13px] text-(--text-3)">
      From history: the tasks of this milestone are gone.
    </p>

    <div v-if="!milestone.issues?.length" class="empty">No issues planned yet.</div>
    <div
      v-else
      class="mt-3.5 overflow-hidden rounded-(--radius-sm) border border-(--border) bg-(--surface) text-[13px] desktop:grid desktop:gap-x-3"
      :class="columns"
      role="table"
      aria-label="Issues"
    >
      <div
        class="hidden px-3.5 py-[9px] text-[11.5px] font-semibold tracking-[0.04em] text-(--text-3) uppercase desktop:col-span-full desktop:grid desktop:grid-cols-subgrid"
        role="row"
        data-heads
      >
        <span role="columnheader">Issue</span>
        <span role="columnheader">State</span>
        <span role="columnheader">MR</span>
        <span role="columnheader" class="text-right">Wall</span>
        <span role="columnheader" class="text-right">Agent</span>
        <span role="columnheader" class="text-right">Paused</span>
        <span role="columnheader" class="text-right">Cost</span>
        <span role="columnheader" class="text-right">Tokens</span>
      </div>
      <template v-for="(row, n) in rows" :key="row.key">
        <!-- A release spans the table: state, version or what is planned,
             who and when, then the why and the operator's reason as a muted
             line. It wraps and keeps state and version first. -->
        <div
          v-if="row.kind === 'release'"
          class="border-(--border) bg-(--surface-2) px-3.5 py-[9px] desktop:col-span-full desktop:border-t"
          :class="{ 'border-t': n > 0 }"
          role="row"
          data-release
        >
          <span class="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1" role="cell">
            <Badge
              :tone="releaseTone(row.release.state)"
              :dot="row.release.state === 'Running'"
              :pulse="row.release.state === 'Running'"
            >
              {{ releaseStateLabel(row.release.state) }}
            </Badge>
            <span
              class="tabular-nums"
              :class="
                releaseMuted(row.release.state)
                  ? 'font-medium text-(--text-3)'
                  : row.release.state === 'Failed'
                    ? 'font-semibold text-(--tone-warning-fg)'
                    : 'font-semibold'
              "
            >
              <ExtLink
                v-if="releaseDone(row.release) && row.release.releaseURL"
                :href="row.release.releaseURL"
                :class="tap"
              >
                {{ releaseHeadline(row.release) }}
              </ExtLink>
              <template v-else>{{ releaseHeadline(row.release) }}</template>
            </span>
            <ExtLink
              v-if="row.release.state === 'Running' && row.release.pipelineURL"
              :href="row.release.pipelineURL"
              :class="tap"
            >
              pipeline
            </ExtLink>
            <span class="whitespace-nowrap text-(--text-3)">by {{ releaseBy(row.release) }}</span>
            <span v-if="releaseTime(row.release)" class="whitespace-nowrap text-(--text-3)">
              <RelTime :time="releaseTime(row.release)" />
            </span>
            <span v-if="releaseNote(row.release)" class="basis-full text-[12.5px] [overflow-wrap:anywhere] text-(--text-3)">
              {{ releaseNote(row.release) }}
            </span>
          </span>
        </div>
        <div
          v-else
          class="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-(--border) px-3.5 py-[9px] desktop:col-span-full desktop:grid desktop:grid-cols-subgrid desktop:border-t"
          :class="{ 'border-t': n > 0 }"
          role="row"
          data-issue
        >
          <span class="flex min-w-0 basis-full items-baseline gap-2 font-semibold desktop:basis-auto" role="cell">
            <span class="font-medium whitespace-nowrap tabular-nums text-(--text-3)" data-iid>#{{ row.issue.iid }}</span>
            <span class="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
              <RouterLink :to="issueTo(row.issue)" class="inline-flex items-center [overflow-wrap:anywhere]" :class="tap">
                {{ row.issue.title || `#${row.issue.iid}` }}
              </RouterLink>
              <Badge
                v-if="row.issue.version"
                tone="info"
                class="self-center tabular-nums"
                :title="`Shipped in ${row.issue.version}`"
              >
                {{ row.issue.version }}
              </Badge>
            </span>
          </span>
          <span class="flex min-w-0 basis-full flex-wrap items-center gap-x-3 gap-y-1.5 desktop:contents" role="none">
            <span role="cell">
              <Badge :tone="issueStateTone(row.issue.state)">{{ row.issue.state }}</Badge>
            </span>
            <span class="whitespace-nowrap" role="cell">
              <ExtLink v-if="row.issue.mergeRequestIID" :href="row.issue.mergeRequestURL" :class="tap">
                !{{ row.issue.mergeRequestIID }}
              </ExtLink>
              <span v-else class="text-(--text-3)">—</span>
            </span>
            <span :class="num" role="cell">
              <span class="text-(--text-3) desktop:hidden">wall </span>{{ issueTime(row.issue, 'wallSec') }}
            </span>
            <span :class="num" role="cell">
              <span class="text-(--text-3) desktop:hidden">agent </span>{{ issueTime(row.issue, 'agentSec') }}
            </span>
            <span :class="num" role="cell">
              <span class="text-(--text-3) desktop:hidden">paused </span>{{ issueTime(row.issue, 'pausedSec') }}
            </span>
            <span
              :class="num"
              role="cell"
              :title="row.issue.figures ? costTooltip(row.issue.figures) : undefined"
            >
              {{ row.issue.figures ? rowCost(row.issue.figures) : '—' }}
            </span>
            <span :class="num" role="cell">
              <span class="text-(--text-3) desktop:hidden">tok </span>{{ row.issue.figures ? rowTokens(row.issue.figures) : '—' }}
            </span>
          </span>
        </div>
      </template>
    </div>

    <template v-if="!milestone.stored">
      <p v-if="waiting" class="mt-3 mb-0 rounded-lg bg-(--tone-progress-bg) px-2.5 py-2">
        ⏳ {{ waiting }}.
        <small class="block text-xs text-(--text-2)">
          Comment <code>/agent start</code> on the tracking issue to plan it now (owners).
        </small>
      </p>
      <div v-if="milestone.autopilot?.items?.length" class="mt-[18px]">
        <h3 :class="sectionHead">
          Queue <span>{{ queueDone(milestone.autopilot.items) }} / {{ milestone.autopilot.items.length }}</span>
        </h3>
        <QueueList :namespace="namespace" :name="name" :milestone="milestone" />
      </div>
      <div v-if="milestone.companions?.length" class="mt-[18px]">
        <h3 :class="sectionHead">Companions <span>{{ milestone.companions.length }}</span></h3>
        <CompanionsList :namespace="namespace" :name="name" :milestone="milestone" />
      </div>
    </template>
  </section>
</template>
