<script setup lang="ts">
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import { computed, defineAsyncComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { hasFeature } from '../api'
import Badge from './Badge.vue'
import Board from './Board.vue'
import CompanionsList from './CompanionsList.vue'
import ExtLink from './ExtLink.vue'
import QueueList from './QueueList.vue'
import RelTime from './RelTime.vue'
import { costTooltip } from '../format'
import { issuesView } from '../timeline'
import { mergedLabel, mergedTooltip, writtenLabel } from '../lines'
import { queueDone, waitingForTurn } from '../milestoneQueue'
import { issueProgress, issueStateTone, issueTime, progressPct, rowCost, rowTokens } from '../milestoneRows'
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

// A milestone's Issues tab (R9, decision 8): its progress and cost (its
// state is the page header's eyebrow, decision 4),
// then one row per issue with its state, merge request, wall, agent and
// paused time, cost and tokens, written and merged lines (R16), and the
// version it shipped in. Releases are
// rows between the issues (R12, decision 15). Below the table the Timeline
// of the issues' runs, or the autopilot queue grouped by lane: running,
// landing, ready to land, waiting, held (with Resume) and done (QueueList
// grouped). The page header holds the
// title. Titles, a release's why and reason are untrusted text and render
// as text.
//
// The table is one grid (docs/contributing/web-ui.md, "Milestone Issues"): from desktop width
// the column heads and every row are subgrids of it, and a release row spans
// all columns. Below it (the rail leaves too little room for ten columns on
// a tablet) the heads are hidden; an issue row shows its number
// and title on the first line, then its state, merge request and labelled
// figures as a wrapping line. Links in rows are 44 px high on phones.
// milestones: the project's, so the milestones it waits for show by title.
const props = defineProps<{ milestone: MilestoneView; namespace: string; name: string; milestones?: { iid: number; title: string }[] }>()

const rows = computed(() => interleaveReleases(props.milestone.issues ?? [], props.milestone.releases))
const progress = computed(() => issueProgress(props.milestone.issues ?? []))
const pct = computed(() => progressPct({ progress: progress.value }))
const waiting = computed(() => waitingForTurn(props.milestone.autopilot?.queuedBehind, props.milestones))

// Below the table: the Timeline, the Lanes or the Board (Decision 15; the
// Board since R14 of "Desk: the Cockpit"), picked by a pill switch in the
// section's toolbar and kept in ?view= (Timeline by default). The Timeline
// needs the history feature, the Board the milestone's tasks (not once
// stored); with one view left there is no switch. The Timeline loads in its
// own chunk.
const MilestoneTimeline = defineAsyncComponent(() => import('./MilestoneTimeline.vue'))
const route = useRoute()
const router = useRouter()
const history = computed(() => hasFeature('history'))
const board = computed(() => !props.milestone.stored)
const view = computed(() => issuesView(route.query.view, history.value, board.value))
const viewItems = computed(() => [
  ...(history.value ? [{ value: 'timeline', label: 'Timeline' }] : []),
  { value: 'lanes', label: 'Lanes' },
  ...(board.value ? [{ value: 'board', label: 'Board' }] : []),
])
const queued = computed(() => !props.milestone.stored && !!props.milestone.autopilot?.items?.length)
function setView(v: string | number) {
  if (!viewItems.value.some((i) => i.value === v)) return
  void router.replace({ query: { ...route.query, view: v } })
}

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
const columns = 'desktop:grid-cols-[minmax(0,1fr)_90px_64px_64px_64px_64px_72px_64px_84px_112px]'
const headText = 'h3 m-0 flex text-[11.5px] font-semibold tracking-[0.06em] text-(--text-3) uppercase'
const sectionHead = `${headText} mb-2 justify-between`
const viewHead = `${headText} gap-2`
</script>

<template>
  <section>
    <div class="flex flex-wrap items-center gap-x-4 gap-y-2 text-[13px]" data-milestone-summary>
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

    <UEmpty v-if="!milestone.issues?.length" icon="i-lucide-list" title="No issues planned yet." variant="naked" size="sm" />
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
        <span role="columnheader" class="text-right" title="Lines the agents wrote">Written</span>
        <span role="columnheader" class="text-right" title="Merged production lines, companions included">Merged</span>
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
              <RouterLink :to="issueTo(row.issue)" class="row-link inline-flex items-center [overflow-wrap:anywhere]" :class="tap">
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
            <span :class="num" role="cell" data-written title="Lines the agents wrote: every step commit, rewrites included">
              <span class="text-(--text-3) desktop:hidden">written </span>{{ writtenLabel(row.issue.figures?.spend) }}
            </span>
            <span
              :class="num"
              role="cell"
              data-merged
              :title="mergedTooltip(row.issue.figures?.mergedLines, row.issue.figures?.companions)"
            >
              <span class="text-(--text-3) desktop:hidden">merged </span>{{ mergedLabel(row.issue.figures?.mergedLines) }}
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
    </template>
    <section v-if="viewItems.length > 1 || queued" class="mt-[18px]" aria-labelledby="issues-view-head" data-issues-view>
      <UDashboardToolbar class="mb-2 min-h-0 flex-wrap border-0 px-0 sm:px-0" :ui="{ left: 'min-w-0' }">
        <template #left>
          <h2 id="issues-view-head" :class="viewHead">
            <template v-if="view === 'timeline'">Timeline</template>
            <template v-else-if="view === 'board'">Board</template>
            <template v-else>
              Queue
              <span v-if="queued">{{ queueDone(milestone.autopilot!.items!) }} / {{ milestone.autopilot!.items!.length }}</span>
            </template>
          </h2>
        </template>
        <template #right>
          <UTabs
            v-if="viewItems.length > 1"
            :model-value="view"
            :items="viewItems"
            variant="pill"
            size="sm"
            :content="false"
            aria-label="View"
            data-issues-view-switch
            @update:model-value="setView"
          />
        </template>
      </UDashboardToolbar>
      <MilestoneTimeline v-if="view === 'timeline'" :namespace="namespace" :name="name" :milestone="milestone.iid" />
      <Board v-else-if="view === 'board'" :tasks="milestone.tasks" />
      <div v-else-if="queued" data-queue>
        <QueueList :namespace="namespace" :name="name" :milestone="milestone" grouped />
      </div>
      <UEmpty v-else icon="i-lucide-list" title="Nothing is queued." variant="naked" size="sm" />
    </section>
    <template v-if="!milestone.stored">
      <div v-if="milestone.companions?.length" class="mt-[18px]">
        <h2 :class="sectionHead">Companions <span>{{ milestone.companions.length }}</span></h2>
        <CompanionsList :namespace="namespace" :name="name" :milestone="milestone" />
      </div>
    </template>
  </section>
</template>
