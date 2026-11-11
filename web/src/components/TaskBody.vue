<script setup lang="ts">
import Markdown from './Markdown.vue'
import { computed } from 'vue'
import { hasFeature } from '../api'
import { ciLabel, ciTone, cost, deferringEngine, phaseTone, shortSHA } from '../format'
import { queueDone } from '../milestoneQueue'
import { useRoute } from 'vue-router'
import { promptOpen, taskPrompts } from '../promptCard'
import { tabQuery, taskTabFromRoute, taskTabs, type TaskTab } from '../streamTabs'
import DataList, { type DataListColumn } from '../ui/DataList.vue'
import RouteTabs from '../ui/RouteTabs.vue'
import Badge from './Badge.vue'
import CostChip from './CostChip.vue'
import CompanionChip from './CompanionChip.vue'
import ExtLink from './ExtLink.vue'
import RelTime from './RelTime.vue'
import StageBadge from './StageBadge.vue'
import Truncated from '../ui/Truncated.vue'
import LiveTranscript from './LiveTranscript.vue'
import Conversation from './Conversation.vue'
import PromptCard from './PromptCard.vue'
import TaskChat from './TaskChat.vue'
import TaskCalls from './TaskCalls.vue'
import type { ProposalView, TaskResponse } from '../types'

// What the Task page holds below its title: stage and links and banners,
// then tabs by ?tab= (R10, decision 11): Overview (the last run's summary,
// prompts, review, CI, plan, autopilot), Chat (chat, conversation), Live,
// Calls and Details (facts, pod, conditions). Only the active tab is
// mounted, so the live transcript and the chat poll only while shown
// (D318-P1). The Task page and the issue page (with a live task) both
// render it from the task they loaded.
const props = defineProps<{ namespace: string; name: string; task: string; data: TaskResponse }>()

const t = computed(() => props.data.task)
const project = computed(() => props.data.project)
const run = computed(() => t.value.status.run ?? {})
const issueURL = (iid: number) => `${project.value?.gitlab.webURL}/-/issues/${iid}`

const autopilotItems = computed(() => t.value.status.autopilot?.items ?? [])
// proposals: the pilot's proposals the chat can accept or reject (those with an ID).
const proposals = computed((): ProposalView[] =>
  (t.value.status.pilot?.proposals ?? []).flatMap((p) => (p.id ? [{ id: p.id, summary: p.summary, postedAt: p.postedAt }] : [])),
)

// Conditions whose True state is the bad one.
const negativeConditions = new Set(['Blocked'])
const conditionTone = (type: string, status: string) => {
  if (status !== 'True' && status !== 'False') return 'muted'
  return (status === 'True') !== negativeConditions.has(type) ? 'success' : 'danger'
}

// Layout classes used more than once. Two sections side by side from the
// desktop width, stacked below it; a grid item's min-w-0 keeps a long value
// from widening the page.
const cols = 'grid gap-4 desktop:grid-cols-2'
const h3 = 'mb-2.5'
// A definition grid (no preflight: m-0 and ms-0 drop the browser margins of
// dl and dd): label above value on a phone, side by side from the
// tablet width. The phone's one column is minmax(0, 1fr), so a nowrap value
// (the kubectl command) scrolls inside its own box instead of widening it.
const dl = 'm-0 grid grid-cols-[minmax(0,1fr)] items-center gap-x-4.5 tablet:grid-cols-[max-content_minmax(0,1fr)] tablet:gap-y-2.5'
const dt = 'text-[13px] text-(--text-3)'
const dd = 'ms-0 mb-2.5 min-w-0 break-words last:mb-0 tablet:mb-0'
const chip = 'mr-1.5 mb-1 inline-block rounded bg-(--surface-2) px-1.5 py-px'

const planColumns: DataListColumn[] = [
  { key: 'iid', label: 'Issue' },
  { key: 'title', label: 'Title' },
  { key: 'dependsOn', label: 'Depends on' },
]
const conditionColumns: DataListColumn[] = [
  { key: 'type', label: 'Condition' },
  { key: 'status', label: 'State' },
]

const resultTone = (r: string | undefined) =>
  r === 'Succeeded' ? 'success' : r === 'NeedsInput' || r === 'Deferred' ? 'warning' : r === 'Failed' ? 'danger' : 'muted'

// Desk chat: compose with permissions.chat; read when the chat feature is on.
const prompts = computed(() => taskPrompts(props.data.prompts))
const showChat = computed(() => !!props.data.permissions?.chat || hasFeature('chat'))

// The tabs this task has content for: Overview and Details always, Chat
// with the chat or the conversation, Live with transcript access, Calls
// with the history database (the calls endpoint answers 404 without it).
const route = useRoute()
const tabs = computed(() => {
  const has: Record<TaskTab, boolean> = {
    overview: true,
    chat: showChat.value || !!props.data.permissions?.gitlab,
    live: !!props.data.permissions?.transcript,
    calls: hasFeature('history'),
    details: true,
  }
  return taskTabs.filter((x) => has[x.id])
})
const tab = computed(() => taskTabFromRoute(route.query.tab, route.hash, tabs.value))
const tabTo = (id: TaskTab) => ({ query: tabQuery(route.query, id, taskTabs) })
// Questions waiting for an answer put their count on Overview.
const badges = computed(() => ({ overview: prompts.value.filter(promptOpen).length }))
const overviewEmpty = computed(
  () => !run.value.summary && !prompts.value.length && t.value.type !== 'Implement' && !t.value.status.plan?.length && !t.value.status.autopilot,
)
// Where the chat's "Live transcript" link goes: the Live tab, on this page.
const transcriptTo = computed(() => (tabs.value.some((x) => x.id === 'live') ? tabTo('live') : undefined))
</script>

<template>
  <div class="mt-2.5 flex flex-wrap items-center gap-x-3.5 gap-y-2">
    <StageBadge :stage="t.stage" />
    <Badge v-if="t.autopilot" tone="progress">Autopilot</Badge>
    <Badge v-if="t.suspend" tone="warning">Suspended</Badge>
    <CostChip :cost="data.cost" />
    <span class="grow" />
    <ExtLink v-if="t.issue" :href="t.issue.webURL || issueURL(t.issue.iid)">Issue</ExtLink>
    <ExtLink v-if="run.mergeRequestURL" :href="run.mergeRequestURL">Merge request !{{ run.mergeRequestIID }}</ExtLink>
    <CompanionChip v-if="t.companion" :companion="t.companion" />
    <ExtLink v-if="t.status.ci?.pipelineURL" :href="t.status.ci.pipelineURL">Pipeline</ExtLink>
  </div>

  <!-- The status line (D319-P1): phase · current step · last result · last
       activity. It wraps on a phone and never scrolls; a long step is
       clamped and opens on tap. -->
  <div data-status-line class="mt-2.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm text-(--text-2)">
    <Badge :tone="phaseTone(t.status.phase)">{{ t.status.phase || '—' }}</Badge>
    <template v-if="run.currentStep">
      <span aria-hidden="true" class="text-(--text-3)">·</span>
      <Truncated class="max-w-full min-w-0 font-[550] text-(--text)" :text="run.currentStep" />
    </template>
    <span aria-hidden="true" class="text-(--text-3)">·</span>
    <Badge :tone="resultTone(run.lastResult)">{{ run.lastResult || 'No run yet' }}</Badge>
    <template v-if="run.lastActivityTime">
      <span aria-hidden="true" class="text-(--text-3)">·</span>
      <span>active <RelTime :time="run.lastActivityTime" /></span>
    </template>
  </div>

  <div v-if="t.runPending && run.deferredUntil" class="banner warning">
    <strong>Waiting for a usage limit</strong>
    <span>
      {{ deferringEngine(run) ?? 'The engine' }} hit its limit; the run starts again
      <RelTime :time="run.deferredUntil" /> (deferred {{ run.deferrals ?? 1 }}×).
    </span>
  </div>
  <div v-if="t.status.phase === 'Blocked'" class="banner danger">
    <strong>Blocked</strong>
    <span><Markdown mode="inline" :text="t.status.message || 'The task hit a safety limit and needs a human.'" /></span>
  </div>
  <div v-else-if="t.status.message" class="banner info"><Markdown mode="inline" :text="t.status.message" /></div>
  <div v-if="t.status.agentTermination?.reason === 'OOMKilled'" class="banner warning">
    <strong>Out of memory</strong>
    <span>
      The agent container was killed <RelTime :time="t.status.agentTermination.finishedAt" /> at a memory limit of
      {{ t.status.agentTermination.memoryLimit || 'unknown' }}<template v-if="t.status.agentMemoryLimit">;
        it now runs with {{ t.status.agentMemoryLimit }}</template
      >.
    </span>
  </div>

  <RouteTabs class="mt-5 w-fit" :tabs="tabs" :active="tab" :to="tabTo" :badges="badges" label="Task" />

  <template v-if="tab === 'overview'">
    <section v-if="run.summary" class="section">
      <h3 :class="h3">Summary of the last run</h3>
      <Markdown class="card px-4 py-3.5" :text="run.summary" />
    </section>

    <section v-if="prompts.length" class="section">
      <h3 :class="h3">Questions from the agent</h3>
      <div class="mb-3 grid gap-2.5">
        <PromptCard v-for="p in prompts" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
      </div>
    </section>

    <div v-if="t.type === 'Implement'" :class="['section', cols]">
      <section class="min-w-0">
        <h3 :class="h3">Review</h3>
        <dl :class="['card px-4 py-3.5', dl]">
          <dt :class="dt">Blocking findings</dt>
          <dd :class="dd">
            <Badge :tone="(run.blockingFindings ?? 0) > 0 ? 'danger' : 'success'">{{ run.blockingFindings ?? 0 }}</Badge>
          </dd>
          <dt :class="dt">Failed reviewers</dt>
          <dd :class="dd">
            <Badge :tone="(run.reviewErrors ?? 0) > 0 ? 'warning' : 'success'">{{ run.reviewErrors ?? 0 }}</Badge>
          </dd>
          <dt :class="dt">Reviewed commit</dt>
          <dd :class="dd">
            <span class="mono">{{ shortSHA(run.reviewedSHA) }}</span>
            <span v-if="run.reviewedSHA && run.pushedSHA && run.reviewedSHA !== run.pushedSHA" class="faint">
              (later commits were not reviewed)
            </span>
          </dd>
        </dl>
      </section>
      <section class="min-w-0">
        <h3 :class="h3">CI</h3>
        <dl v-if="t.status.ci" :class="['card px-4 py-3.5', dl]">
          <dt :class="dt">Pipeline</dt>
          <dd :class="dd">
            <Badge :tone="ciTone(t.status.ci.state)">{{ ciLabel[t.status.ci.state] }}</Badge>
            <ExtLink v-if="t.status.ci.pipelineURL" :href="t.status.ci.pipelineURL" class="ml-2">
              #{{ t.status.ci.pipelineID }}
            </ExtLink>
          </dd>
          <dt :class="dt">Commit</dt>
          <dd :class="[dd, 'mono']">{{ shortSHA(t.status.ci.sha) }}</dd>
          <dt :class="dt">Fix rounds</dt>
          <dd :class="dd">{{ run.ciRound ?? 0 }}<span v-if="t.status.ci.retried" class="faint"> · failed jobs retried</span></dd>
          <template v-if="t.status.ci.failedJobs?.length">
            <dt :class="dt">Failed jobs</dt>
            <dd :class="dd">
              <code v-for="j in t.status.ci.failedJobs" :key="j" :class="chip">{{ j }}</code>
            </dd>
          </template>
        </dl>
        <div v-else class="card empty">No pipeline followed yet.</div>
      </section>
    </div>

    <section v-if="t.status.plan?.length" class="section">
      <h3 :class="h3">Plan</h3>
      <div class="card">
        <DataList :columns="planColumns" :rows="t.status.plan" :row-key="(pi) => pi.key">
          <template #cell-iid="{ row: pi }"><ExtLink :href="issueURL(pi.iid)">#{{ pi.iid }}</ExtLink></template>
          <template #cell-dependsOn="{ row: pi }">
            <span v-if="!pi.dependsOn?.length" class="faint">—</span>
            <span v-for="d in pi.dependsOn" :key="d" :class="chip">#{{ d }}</span>
          </template>
        </DataList>
      </div>
    </section>

    <section v-if="t.status.autopilot" class="section">
      <div class="row section-head">
        <h3>Autopilot</h3>
        <Badge :tone="t.status.autopilot.phase === 'Paused' ? 'warning' : 'progress'">{{ t.status.autopilot.phase }}</Badge>
        <span v-if="autopilotItems.length" class="faint">{{ queueDone(autopilotItems) }} / {{ autopilotItems.length }} done</span>
        <span class="grow" />
        <RouterLink
          v-if="t.milestone"
          :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(t.milestone.iid) } }"
        >
          Queue in the milestone stream
        </RouterLink>
        <ExtLink v-if="t.status.autopilot.trackingIssueIID" :href="issueURL(t.status.autopilot.trackingIssueIID)">
          Tracking issue #{{ t.status.autopilot.trackingIssueIID }}
        </ExtLink>
      </div>
      <div v-if="t.status.autopilot.pausedReason" class="banner warning">
        <span>
          <Markdown mode="inline" :text="t.status.autopilot.pausedReason" />
          <template v-if="t.status.autopilot.pausedFor">(issue #{{ t.status.autopilot.pausedFor }})</template>
          <template v-if="t.status.autopilot.resumeWhenReady">
            It continues by itself once a later run of #{{ t.status.autopilot.pausedFor }} is reviewed and green.
          </template>
        </span>
      </div>
    </section>

    <div v-if="overviewEmpty" class="card empty section">No run has left a summary yet.</div>
  </template>

  <template v-else-if="tab === 'chat'">
    <section v-if="showChat" class="section">
      <h3 :class="h3">Chat with the agent</h3>
      <TaskChat
        :namespace="namespace"
        :name="name"
        :task="task"
        :can-post="!!data.permissions?.chat"
        :transcript="transcriptTo"
        :phase="t.status.phase"
        :message="t.status.message"
        :pod-name="t.status.podName"
        :task-type="t.type"
        :autopilot-phase="t.status.autopilot?.phase"
        :autopilot-items="t.status.autopilot?.items"
        :proposals="proposals"
      />
    </section>

    <section v-if="data.permissions?.gitlab" class="section">
      <h3 :class="h3">Conversation</h3>
      <Conversation
        :namespace="namespace"
        :name="name"
        :task="task"
        :steer="!!data.permissions?.steer"
        :stamp="`${run.observedRunRequest ?? ''}|${run.activeRunRequest ?? ''}|${run.mergeRequestIID ?? ''}|${t.status.ci?.state ?? ''}|${t.status.autopilot?.phase ?? ''}`"
      />
    </section>
  </template>

  <section v-else-if="tab === 'live'" id="live-transcript" class="section">
    <h3 :class="h3">Live</h3>
    <LiveTranscript
      :namespace="namespace"
      :name="name"
      :task="task"
      :pod="t.status.podName"
      :run="String(run.activeRunRequest ?? '')"
    />
  </section>

  <TaskCalls v-else-if="tab === 'calls'" :namespace="namespace" :name="name" :task="task" />

  <template v-else>
    <!-- Phase, current step, last result and last activity are the
         header's status line; the other facts are here. -->
    <div class="facts section">
      <div class="fact">
        <div class="label">Runs</div>
        <Truncated class="font-[550]">
          {{ run.runCount ?? 0 }}<span v-if="t.runPending" class="faint"> + 1 requested</span>
        </Truncated>
      </div>
      <div class="fact">
        <div class="label">Model spend</div>
        <Truncated class="font-[550]" :text="cost(run.costUSD)" />
      </div>
      <div v-if="t.type === 'Implement'" class="fact">
        <div class="label">Branch</div>
        <Truncated class="mono font-[550]" :text="run.branch || '—'" />
      </div>
      <div v-if="t.type === 'Implement'" class="fact">
        <div class="label">Last push</div>
        <Truncated class="font-[550]">
          <span class="mono">{{ shortSHA(run.pushedSHA) }}</span>
          <span v-if="run.pushedAt" class="faint"> · <RelTime :time="run.pushedAt" /></span>
        </Truncated>
      </div>
    </div>

    <div :class="['section', cols]">
      <section class="min-w-0">
        <h3 :class="h3">Pod</h3>
        <dl :class="['card px-4 py-3.5', dl]">
          <dt :class="dt">Pod</dt>
          <dd :class="[dd, 'mono']">{{ t.status.podName || 'not running' }}</dd>
          <dt :class="dt">Workspace</dt>
          <dd :class="[dd, 'mono']">{{ t.status.workspaceClaimName || '—' }}</dd>
          <template v-if="t.status.agentTermination">
            <dt :class="dt">Agent container</dt>
            <dd :class="dd">
              last stopped {{ t.status.agentTermination.reason }} (exit {{ t.status.agentTermination.exitCode ?? '?' }}),
              {{ t.status.agentTermination.restarts ?? 0 }} restarts
            </dd>
          </template>
          <template v-if="t.status.podName">
            <dt :class="dt">Logs</dt>
            <dd :class="dd">
              <!-- Scrolls inside its own box; the page never does. -->
              <code class="block overflow-x-auto rounded bg-(--surface-2) px-2 py-1.5 whitespace-nowrap"
                >kubectl -n {{ t.namespace }} logs -f {{ t.status.podName }} -c runner</code
              >
            </dd>
          </template>
        </dl>
      </section>
      <section v-if="t.status.conditions?.length" class="min-w-0">
        <h3 :class="h3">Conditions</h3>
        <div class="card">
          <DataList :columns="conditionColumns" :rows="t.status.conditions" :row-key="(c) => c.type">
            <template #cell-status="{ row: c }">
              <Badge :tone="conditionTone(c.type, c.status)">{{ c.reason || c.status }}</Badge>
              <div v-if="c.message" class="faint">{{ c.message }}</div>
            </template>
          </DataList>
        </div>
      </section>
    </div>
  </template>
</template>
