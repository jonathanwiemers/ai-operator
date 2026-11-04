<script setup lang="ts">
import { computed } from 'vue'
import { hasFeature } from '../api'
import { useResource } from '../live'
import { ciLabel, ciTone, cost, phaseTone, shortSHA } from '../format'
import { queueDone } from '../milestoneQueue'
import Badge from '../components/Badge.vue'
import CostChip from '../components/CostChip.vue'
import ExtLink from '../components/ExtLink.vue'
import RelTime from '../components/RelTime.vue'
import StageBadge from '../components/StageBadge.vue'
import LiveTranscript from '../components/LiveTranscript.vue'
import Conversation from '../components/Conversation.vue'
import TaskChat from '../components/TaskChat.vue'
import NotFound from './NotFound.vue'
import type { TaskResponse } from '../types'

const props = defineProps<{ namespace: string; name: string; task: string }>()
const { data, error, notFound, loading } = useResource<TaskResponse>(
  () =>
    `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/tasks/${encodeURIComponent(props.task)}`,
)

const t = computed(() => data.value?.task)
const project = computed(() => data.value?.project)
const run = computed(() => t.value?.status.run ?? {})
const issueURL = (iid: number) => `${project.value?.gitlab.webURL}/-/issues/${iid}`

const heading = computed(() => {
  const v = t.value
  if (!v) return ''
  if (v.issue) return v.issue.title || `Issue #${v.issue.iid}`
  return `Plan: ${v.milestone?.title ?? v.name}`
})

const autopilotItems = computed(() => t.value?.status.autopilot?.items ?? [])

// Conditions whose True state is the bad one.
const negativeConditions = new Set(['Blocked'])
const conditionTone = (type: string, status: string) => {
  if (status !== 'True' && status !== 'False') return 'muted'
  return (status === 'True') !== negativeConditions.has(type) ? 'success' : 'danger'
}

const resultTone = (r: string | undefined) =>
  r === 'Succeeded' ? 'success' : r === 'NeedsInput' || r === 'Deferred' ? 'warning' : r === 'Failed' ? 'danger' : 'muted'

// Desk chat: compose with permissions.chat; read when the chat feature is on.
const showChat = computed(() => !!data.value?.permissions?.chat || hasFeature('chat'))
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <nav class="crumbs muted">
      <RouterLink :to="{ name: 'stream-project', params: { namespace, name } }">{{ name }}</RouterLink>
      <span>/</span>
      <RouterLink :to="{ name: 'stream-project', params: { namespace, name }, query: { tab: 'board' } }">Board</RouterLink>
      <span>/</span>
      <span class="mono">{{ task }}</span>
    </nav>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !t" class="card empty">Loading…</div>

    <template v-if="t">
      <div class="head">
        <div class="row">
          <span v-if="t.issue" class="iid">#{{ t.issue.iid }}</span>
          <h1>{{ heading }}</h1>
        </div>
        <div class="row links">
          <StageBadge :stage="t.stage" />
          <Badge v-if="t.autopilot" tone="progress">Autopilot</Badge>
          <Badge v-if="t.suspend" tone="warning">Suspended</Badge>
          <CostChip :cost="data?.cost" />
          <span class="spacer" />
          <ExtLink v-if="t.issue" :href="t.issue.webURL || issueURL(t.issue.iid)">Issue</ExtLink>
          <ExtLink v-if="run.mergeRequestURL" :href="run.mergeRequestURL">Merge request !{{ run.mergeRequestIID }}</ExtLink>
          <ExtLink v-if="t.status.ci?.pipelineURL" :href="t.status.ci.pipelineURL">Pipeline</ExtLink>
        </div>
      </div>

      <div v-if="t.runPending && run.deferredUntil" class="banner warning">
        <strong>Waiting for a usage limit</strong>
        <span>
          {{ run.limitedEngine || 'The engine' }} hit its limit; the run starts again
          <RelTime :time="run.deferredUntil" /> (deferred {{ run.deferrals ?? 1 }}×).
        </span>
      </div>
      <div v-if="t.status.phase === 'Blocked'" class="banner danger">
        <strong>Blocked</strong>
        <span>{{ t.status.message || 'The task hit a safety limit and needs a human.' }}</span>
      </div>
      <div v-else-if="t.status.message" class="banner info">{{ t.status.message }}</div>
      <div v-if="t.status.agentTermination?.reason === 'OOMKilled'" class="banner warning">
        <strong>Out of memory</strong>
        <span>
          The agent container was killed <RelTime :time="t.status.agentTermination.finishedAt" /> at a memory limit of
          {{ t.status.agentTermination.memoryLimit || 'unknown' }}<template v-if="t.status.agentMemoryLimit">;
            it now runs with {{ t.status.agentMemoryLimit }}</template
          >.
        </span>
      </div>

      <div class="facts section">
        <div class="fact">
          <div class="label">Phase</div>
          <div class="value"><Badge :tone="phaseTone(t.status.phase)">{{ t.status.phase || '—' }}</Badge></div>
        </div>
        <div class="fact">
          <div class="label">Current step</div>
          <div class="value" :title="run.currentStep">{{ run.currentStep || '—' }}</div>
        </div>
        <div class="fact">
          <div class="label">Last result</div>
          <div class="value">
            <Badge :tone="resultTone(run.lastResult)">{{ run.lastResult || 'No run yet' }}</Badge>
          </div>
        </div>
        <div class="fact">
          <div class="label">Runs</div>
          <div class="value">
            {{ run.runCount ?? 0 }}<span v-if="t.runPending" class="faint"> + 1 requested</span>
          </div>
        </div>
        <div class="fact">
          <div class="label">Last activity</div>
          <div class="value"><RelTime :time="run.lastActivityTime" /></div>
        </div>
        <div class="fact">
          <div class="label">Model spend</div>
          <div class="value">{{ cost(run.costUSD) }}</div>
        </div>
        <div v-if="t.type === 'Implement'" class="fact">
          <div class="label">Branch</div>
          <div class="value mono" :title="run.branch">{{ run.branch || '—' }}</div>
        </div>
        <div v-if="t.type === 'Implement'" class="fact">
          <div class="label">Last push</div>
          <div class="value">
            <span class="mono">{{ shortSHA(run.pushedSHA) }}</span>
            <span v-if="run.pushedAt" class="faint"> · <RelTime :time="run.pushedAt" /></span>
          </div>
        </div>
      </div>

      <section v-if="showChat" class="section">
        <h3>Chat with the agent</h3>
        <TaskChat
          :namespace="namespace"
          :name="name"
          :task="task"
          :can-post="!!data?.permissions?.chat"
          :can-transcript="!!data?.permissions?.transcript"
          :phase="t.status.phase"
          :message="t.status.message"
          :pod-name="t.status.podName"
          :task-type="t.type"
          :autopilot-phase="t.status.autopilot?.phase"
          :autopilot-items="t.status.autopilot?.items"
          :proposals="t.status.pilot?.proposals"
        />
      </section>

      <section v-if="data?.permissions?.gitlab" class="section">
        <h3>Conversation</h3>
        <Conversation
          :namespace="namespace"
          :name="name"
          :task="task"
          :steer="!!data?.permissions?.steer"
          :stamp="`${run.observedRunRequest ?? ''}|${run.activeRunRequest ?? ''}|${run.mergeRequestIID ?? ''}|${t.status.ci?.state ?? ''}|${t.status.autopilot?.phase ?? ''}`"
        />
      </section>

      <section v-if="data?.permissions?.transcript" id="live-transcript" class="section">
        <h3>Live</h3>
        <LiveTranscript
          :namespace="namespace"
          :name="name"
          :task="task"
          :pod="t.status.podName"
          :run="String(run.activeRunRequest ?? '')"
        />
      </section>

      <section v-if="run.summary" class="section">
        <h3>Summary of the last run</h3>
        <div class="card body prose">{{ run.summary }}</div>
      </section>

      <div v-if="t.type === 'Implement'" class="cols section">
        <section>
          <h3>Review</h3>
          <div class="card body kv">
            <div>Blocking findings</div>
            <div>
              <Badge :tone="(run.blockingFindings ?? 0) > 0 ? 'danger' : 'success'">{{ run.blockingFindings ?? 0 }}</Badge>
            </div>
            <div>Failed reviewers</div>
            <div>
              <Badge :tone="(run.reviewErrors ?? 0) > 0 ? 'warning' : 'success'">{{ run.reviewErrors ?? 0 }}</Badge>
            </div>
            <div>Reviewed commit</div>
            <div>
              <span class="mono">{{ shortSHA(run.reviewedSHA) }}</span>
              <span v-if="run.reviewedSHA && run.pushedSHA && run.reviewedSHA !== run.pushedSHA" class="faint">
                (later commits were not reviewed)
              </span>
            </div>
          </div>
        </section>
        <section>
          <h3>CI</h3>
          <div v-if="t.status.ci" class="card body kv">
            <div>Pipeline</div>
            <div>
              <Badge :tone="ciTone(t.status.ci.state)">{{ ciLabel[t.status.ci.state] }}</Badge>
              <ExtLink v-if="t.status.ci.pipelineURL" :href="t.status.ci.pipelineURL" class="pl">
                #{{ t.status.ci.pipelineID }}
              </ExtLink>
            </div>
            <div>Commit</div>
            <div class="mono">{{ shortSHA(t.status.ci.sha) }}</div>
            <div>Fix rounds</div>
            <div>{{ run.ciRound ?? 0 }}<span v-if="t.status.ci.retried" class="faint"> · failed jobs retried</span></div>
            <template v-if="t.status.ci.failedJobs?.length">
              <div>Failed jobs</div>
              <div>
                <code v-for="j in t.status.ci.failedJobs" :key="j" class="job">{{ j }}</code>
              </div>
            </template>
          </div>
          <div v-else class="card empty">No pipeline followed yet.</div>
        </section>
      </div>

      <section v-if="t.status.plan?.length" class="section">
        <h3>Plan</h3>
        <div class="card">
          <table class="list">
            <thead>
              <tr>
                <th>Issue</th>
                <th>Title</th>
                <th>Depends on</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="pi in t.status.plan" :key="pi.key">
                <td><ExtLink :href="issueURL(pi.iid)">#{{ pi.iid }}</ExtLink></td>
                <td>{{ pi.title }}</td>
                <td>
                  <span v-if="!pi.dependsOn?.length" class="faint">—</span>
                  <span v-for="d in pi.dependsOn" :key="d" class="dep">#{{ d }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section v-if="t.status.autopilot" class="section">
        <div class="row section-head">
          <h3>Autopilot</h3>
          <Badge :tone="t.status.autopilot.phase === 'Paused' ? 'warning' : 'progress'">{{ t.status.autopilot.phase }}</Badge>
          <span v-if="autopilotItems.length" class="faint">{{ queueDone(autopilotItems) }} / {{ autopilotItems.length }} done</span>
          <span class="spacer" />
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
            {{ t.status.autopilot.pausedReason }}
            <template v-if="t.status.autopilot.pausedFor">(issue #{{ t.status.autopilot.pausedFor }})</template>
            <template v-if="t.status.autopilot.resumeWhenReady">
              It continues by itself once a later run of #{{ t.status.autopilot.pausedFor }} is reviewed and green.
            </template>
          </span>
        </div>
      </section>

      <div class="cols section">
        <section>
          <h3>Pod</h3>
          <div class="card body kv">
            <div>Pod</div>
            <div class="mono">{{ t.status.podName || 'not running' }}</div>
            <div>Workspace</div>
            <div class="mono">{{ t.status.workspaceClaimName || '—' }}</div>
            <template v-if="t.status.agentTermination">
              <div>Agent container</div>
              <div>
                last stopped {{ t.status.agentTermination.reason }} (exit {{ t.status.agentTermination.exitCode ?? '?' }}),
                {{ t.status.agentTermination.restarts ?? 0 }} restarts
              </div>
            </template>
            <template v-if="t.status.podName">
              <div>Logs</div>
              <div>
                <code class="cmd">kubectl -n {{ t.namespace }} logs -f {{ t.status.podName }} -c runner</code>
              </div>
            </template>
          </div>
        </section>
        <section v-if="t.status.conditions?.length">
          <h3>Conditions</h3>
          <div class="card">
            <table class="list">
              <tbody>
                <tr v-for="c in t.status.conditions" :key="c.type">
                  <td>{{ c.type }}</td>
                  <td>
                    <Badge :tone="conditionTone(c.type, c.status)">
                      {{ c.reason || c.status }}
                    </Badge>
                    <div v-if="c.message" class="faint">{{ c.message }}</div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </template>
  </div>
</template>

<style scoped>
.crumbs {
  display: flex;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 10px;
  flex-wrap: wrap;
}

.head h1 {
  overflow-wrap: anywhere;
}

.iid {
  font-family: var(--mono);
  font-size: 18px;
  color: var(--text-3);
}

.links {
  margin-top: 10px;
  gap: 14px;
}

.section h3 {
  margin-bottom: 10px;
}

.body {
  padding: 14px 16px;
}

.cols {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(360px, 1fr));
  gap: 16px;
}

.kv {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 10px 18px;
  align-items: center;
}

.kv > div:nth-child(odd) {
  color: var(--text-3);
  font-size: 13px;
}

.pl {
  margin-left: 8px;
}

.job,
.dep {
  display: inline-block;
  margin: 0 6px 4px 0;
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--surface-2);
}

.cmd {
  display: block;
  overflow-x: auto;
  white-space: nowrap;
  padding: 6px 8px;
  border-radius: 4px;
  background: var(--surface-2);
}
</style>
