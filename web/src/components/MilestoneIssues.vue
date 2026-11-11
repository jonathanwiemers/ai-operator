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
</script>

<template>
  <section class="missues">
    <div class="sum">
      <Badge :tone="stateTone(milestone.state)">{{ milestone.state }}</Badge>
      <span class="progress" :title="`${pct}% merged`">
        <span
          class="bar"
          role="progressbar"
          :aria-valuenow="pct"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-label="`${pct}% merged`"
        >
          <span :style="{ width: `${pct}%` }" />
        </span>
        <span class="num">{{ progress.merged }}/{{ progress.total }} merged</span>
      </span>
      <span class="num" :title="costTooltip(milestone)">{{ rowCost(milestone) }}</span>
      <span class="num">{{ rowTokens(milestone) }} tok</span>
    </div>
    <p v-if="milestone.stored" class="note">From history: the tasks of this milestone are gone.</p>

    <div v-if="!milestone.issues?.length" class="empty">No issues planned yet.</div>
    <div v-else class="issue-table" role="table" aria-label="Issues">
      <div class="row head" role="row">
        <span role="columnheader">Issue</span>
        <span role="columnheader">State</span>
        <span role="columnheader">MR</span>
        <span role="columnheader" class="r">Wall</span>
        <span role="columnheader" class="r">Agent</span>
        <span role="columnheader" class="r">Paused</span>
        <span role="columnheader" class="r">Cost</span>
        <span role="columnheader" class="r">Tokens</span>
      </div>
      <template v-for="row in rows" :key="row.key">
        <div
          v-if="row.kind === 'release'"
          class="row rel"
          :class="{ muted: releaseMuted(row.release.state), failed: row.release.state === 'Failed' }"
          role="row"
        >
          <span class="relmain" role="cell">
            <Badge
              :tone="releaseTone(row.release.state)"
              :dot="row.release.state === 'Running'"
              :pulse="row.release.state === 'Running'"
            >
              {{ releaseStateLabel(row.release.state) }}
            </Badge>
            <span class="relhead">
              <ExtLink v-if="releaseDone(row.release) && row.release.releaseURL" :href="row.release.releaseURL">
                {{ releaseHeadline(row.release) }}
              </ExtLink>
              <template v-else>{{ releaseHeadline(row.release) }}</template>
            </span>
            <ExtLink v-if="row.release.state === 'Running' && row.release.pipelineURL" :href="row.release.pipelineURL">
              pipeline
            </ExtLink>
            <span class="relby">by {{ releaseBy(row.release) }}</span>
            <span v-if="releaseTime(row.release)" class="reltime"><RelTime :time="releaseTime(row.release)" /></span>
            <span v-if="releaseNote(row.release)" class="relwhy">{{ releaseNote(row.release) }}</span>
          </span>
        </div>
        <div v-else class="row" role="row">
          <span class="title" role="cell">
            <span class="iid">#{{ row.issue.iid }}</span>
            <RouterLink :to="issueTo(row.issue)">{{ row.issue.title || `#${row.issue.iid}` }}</RouterLink>
            <Badge v-if="row.issue.version" tone="info" class="ver" :title="`Shipped in ${row.issue.version}`">
              {{ row.issue.version }}
            </Badge>
          </span>
          <span class="state" role="cell">
            <Badge :tone="issueStateTone(row.issue.state)">{{ row.issue.state }}</Badge>
          </span>
          <span class="mr" role="cell">
            <ExtLink v-if="row.issue.mergeRequestIID" :href="row.issue.mergeRequestURL">
              !{{ row.issue.mergeRequestIID }}
            </ExtLink>
            <span v-else class="dash">—</span>
          </span>
          <span class="num wall" role="cell"><span class="lbl">wall </span>{{ issueTime(row.issue, 'wallSec') }}</span>
          <span class="num agent" role="cell">
            <span class="lbl">agent </span>{{ issueTime(row.issue, 'agentSec') }}
          </span>
          <span class="num paused" role="cell">
            <span class="lbl">paused </span>{{ issueTime(row.issue, 'pausedSec') }}
          </span>
          <span
            class="num cost"
            role="cell"
            :title="row.issue.figures ? costTooltip(row.issue.figures) : undefined"
          >
            {{ row.issue.figures ? rowCost(row.issue.figures) : '—' }}
          </span>
          <span class="num tokens" role="cell">
            <span class="lbl">tok </span>{{ row.issue.figures ? rowTokens(row.issue.figures) : '—' }}
          </span>
        </div>
      </template>
    </div>

    <template v-if="!milestone.stored">
      <p v-if="waiting" class="waiting">
        ⏳ {{ waiting }}.
        <small>Comment <code>/agent start</code> on the tracking issue to plan it now (owners).</small>
      </p>
      <div v-if="milestone.autopilot?.items?.length" class="block">
        <h3>Queue <span>{{ queueDone(milestone.autopilot.items) }} / {{ milestone.autopilot.items.length }}</span></h3>
        <QueueList :namespace="namespace" :name="name" :milestone="milestone" />
      </div>
      <div v-if="milestone.companions?.length" class="block">
        <h3>Companions <span>{{ milestone.companions.length }}</span></h3>
        <CompanionsList :namespace="namespace" :name="name" :milestone="milestone" />
      </div>
    </template>
  </section>
</template>

<style scoped>
.sum {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
  font-size: 13px;
}
.progress {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 200px;
}
.bar {
  flex: 1;
  height: 6px;
  border-radius: 999px;
  background: var(--surface-3);
  overflow: hidden;
}
.bar > span {
  display: block;
  height: 100%;
  background: var(--tone-success-fg);
}
.num {
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
  white-space: nowrap;
}
.note {
  margin: 10px 0 0;
  font-size: 13px;
  color: var(--text-3);
}
.issue-table {
  margin-top: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
}
.row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 90px 64px 64px 64px 64px 72px 64px;
  align-items: center;
  gap: 12px;
  padding: 9px 14px;
  border-top: 1px solid var(--border);
  font-size: 13px;
}
.row:first-child {
  border-top: 0;
}
.row.head {
  font-size: 11.5px;
  font-weight: 600;
  color: var(--text-3);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}
.r,
.row .num {
  text-align: right;
}
.title {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.iid {
  font-weight: 500;
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
}
.dash {
  color: var(--text-3);
}
.lbl {
  display: none;
  color: var(--text-3);
}
.ver {
  align-self: center;
  font-variant-numeric: tabular-nums;
}
/* A release row spans the table: state, version or what is planned, who
   and when, then the why and the operator's reason as a muted line. It
   wraps on narrow screens and keeps state and version first. */
.row.rel {
  display: block;
  background: var(--surface-2);
}
.relmain {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 10px;
  min-width: 0;
}
.relhead {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.relby,
.reltime {
  color: var(--text-3);
  white-space: nowrap;
}
.relwhy {
  flex-basis: 100%;
  color: var(--text-3);
  font-size: 12.5px;
  overflow-wrap: anywhere;
}
.row.rel.muted .relhead {
  color: var(--text-3);
  font-weight: 500;
}
.row.rel.failed .relhead {
  color: var(--tone-warning-fg);
}
.empty {
  padding: 32px;
  text-align: center;
  color: var(--text-3);
}
/* Narrow screens: each issue is a card, title and state on top, the
   figures below, labelled. */
@media (max-width: 760px) {
  .row.head {
    display: none;
  }
  .row {
    grid-template-columns: repeat(3, minmax(0, 1fr));
    grid-template-areas:
      'title title state'
      'mr cost tokens'
      'wall agent paused';
    gap: 6px 12px;
  }
  .title {
    grid-area: title;
  }
  .state {
    grid-area: state;
    justify-self: end;
  }
  .mr {
    grid-area: mr;
  }
  .wall {
    grid-area: wall;
  }
  .agent {
    grid-area: agent;
  }
  .paused {
    grid-area: paused;
  }
  .cost {
    grid-area: cost;
  }
  .tokens {
    grid-area: tokens;
  }
  .row .num {
    text-align: left;
  }
  .lbl {
    display: inline;
  }
}
.waiting {
  margin: 12px 0 0;
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--tone-progress-bg);
}
.waiting small {
  display: block;
  color: var(--text-2);
  font-size: 12px;
}
.block {
  margin-top: 18px;
}
.block h3 {
  font-size: 11.5px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-3);
  margin: 0 0 8px;
  display: flex;
  justify-content: space-between;
  font-weight: 600;
}
</style>
