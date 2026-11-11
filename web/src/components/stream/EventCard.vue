<script setup lang="ts">
import { computed, ref } from 'vue'
import { ApiError, post } from '../../api'
import { ciLabel, ciTone, clockTime, cost, fullTime, duration, shortSHA, type Tone } from '../../format'
import { useNow } from '../../live'
import type { CIState, ProjectDetail, PromptView, StreamEvent } from '../../types'
import { promptForEvent } from '../../stream'
import {
  isReleaseEvent,
  releaseEventBadge,
  releaseEventIssues,
  releaseEventText,
  releaseEventTitle,
} from '../../releaseEvents'
import Badge from '../Badge.vue'
import ExtLink from '../ExtLink.vue'
import Markdown from '../Markdown.vue'
import PromptCardCompact from '../PromptCardCompact.vue'
import NoteThread from './NoteThread.vue'

const props = defineProps<{
  event?: StreamEvent
  notes?: StreamEvent[]
  project?: ProjectDetail | null
  canSteer?: boolean
  prompts?: PromptView[]
}>()
const emit = defineEmits<{ posted: [] }>()
const now = useNow()
const posting = ref(false)
const sendError = ref('')

const e = computed(() => props.event)
const prompt = computed(() => (props.event ? promptForEvent(props.event, props.prompts) : undefined))
const isPrompt = computed(() => !!e.value?.kind.startsWith('prompt.'))
const isRelease = computed(() => !!e.value && isReleaseEvent(e.value))
const releaseBadge = computed(() => (e.value ? releaseEventBadge(e.value) : undefined))

// promptTo: the stream of the prompt's milestone (or project), at its card.
function promptTo(ev: StreamEvent) {
  const hash = '#prompt-' + (ev.data.promptID ?? '')
  const params = { namespace: ev.namespace, name: ev.project }
  return ev.milestoneIID
    ? { name: 'stream-milestone' as const, params: { ...params, iid: ev.milestoneIID }, hash }
    : { name: 'stream-project' as const, params, hash }
}
// retroTo: the Retrospective tab of the event's milestone.
function retroTo(ev: StreamEvent) {
  return {
    name: 'stream-milestone' as const,
    params: { namespace: ev.namespace, name: ev.project, iid: ev.milestoneIID },
    query: { tab: 'retrospective' },
  }
}
const taskRunning = computed(() => {
  const ev = e.value
  if (!ev || ev.kind !== 'run.started' || !ev.task || !props.project) return false
  const t = [...(props.project.milestones ?? []).flatMap((m) => m.tasks), ...(props.project.unplanned ?? [])].find(
    (x) => x.name === ev.task,
  )
  return t?.runPending || t?.status.phase === 'Running'
})

function resultTone(r: string | undefined): Tone {
  switch (r) {
    case 'Succeeded':
      return 'success'
    case 'NeedsInput':
      return 'warning'
    case 'Failed':
      return 'danger'
    case 'Deferred':
      return 'muted'
    default:
      return 'neutral'
  }
}

async function decide(yes: boolean) {
  const ev = e.value
  if (!ev?.milestoneIID || ev.data.proposalID == null || posting.value) return
  const body = `${yes ? 'yes' : 'no'} P${ev.data.proposalID}`
  posting.value = true
  sendError.value = ''
  try {
    await post(
      `/projects/${encodeURIComponent(ev.namespace)}/${encodeURIComponent(ev.project)}/milestones/${ev.milestoneIID}/notes`,
      { body },
    )
    emit('posted')
  } catch (err) {
    sendError.value = err instanceof ApiError || err instanceof Error ? err.message : String(err)
  } finally {
    posting.value = false
  }
}

function taskLink(ev: StreamEvent) {
  if (!ev.task) return undefined
  return {
    name: 'task' as const,
    params: { namespace: ev.namespace, name: ev.project, task: ev.task },
  }
}
</script>

<template>
  <NoteThread v-if="notes" :notes="notes" />
  <div v-else-if="e" class="ev" :class="{ warn: e.kind === 'needs' }">
    <div class="av sys">
      <template v-if="e.kind === 'run.started' || e.kind === 'run.finished'">▶</template>
      <template v-else-if="e.kind === 'ci'">✕</template>
      <template v-else-if="e.kind === 'review'">◎</template>
      <template v-else-if="e.kind === 'merged'">⇡</template>
      <template v-else-if="e.kind === 'needs'">!</template>
      <template v-else-if="e.kind === 'proposal'">P</template>
      <template v-else-if="e.kind === 'autopilot'">◇</template>
      <template v-else-if="e.kind === 'retrospective'">R</template>
      <template v-else-if="e.kind === 'checkpoint'">💾</template>
      <template v-else-if="isRelease">⚑</template>
      <template v-else-if="isPrompt">?</template>
      <template v-else-if="e.kind === 'chat.user' || e.kind === 'chat.agent'">C</template>
      <template v-else>·</template>
    </div>
    <div>
      <div class="meta">
        <b>{{ (isRelease && releaseEventTitle(e)) || e.summary || e.kind }}</b>
        <span v-if="e.issueIID" class="ctx">#{{ e.issueIID }}</span>
        <span v-else-if="e.mergeRequestIID" class="ctx">!{{ e.mergeRequestIID }}</span>
        <time :datetime="e.at" :title="fullTime(e.at)">{{ clockTime(e.at, now) }}</time>
        <Badge v-if="e.kind === 'run.started' && taskRunning" tone="progress" dot pulse>live</Badge>
      </div>

      <!-- run.finished -->
      <div v-if="e.kind === 'run.finished'" class="compact">
        <Badge :tone="resultTone(e.data.result)">{{ e.data.result || 'done' }}</Badge>
        <span v-if="e.data.runSummary"><Markdown mode="inline" :text="e.data.runSummary" /></span>
        <span class="muted">
          <template v-if="e.data.costUSD">{{ cost(e.data.costUSD) }}</template>
          <template v-if="e.data.durationSec != null">
            · {{ duration(e.data.durationSec * 1000) }}
          </template>
        </span>
        <RouterLink v-if="taskLink(e)" class="t" :to="taskLink(e)!">task</RouterLink>
      </div>

      <!-- run.started -->
      <div v-else-if="e.kind === 'run.started'" class="compact">
        <Badge tone="progress">started</Badge>
        <span v-if="e.data.request != null">request {{ e.data.request }}</span>
        <RouterLink v-if="taskLink(e)" class="t" :to="taskLink(e)!">Open live</RouterLink>
      </div>

      <!-- push -->
      <div v-else-if="e.kind === 'push'" class="compact">
        <Badge tone="info">push</Badge>
        <code class="mono">{{ shortSHA(e.data.sha) }}</code>
        <span v-if="e.data.branch" class="muted">{{ e.data.branch }}</span>
      </div>

      <!-- checkpoint: one no call carries (budget stop, lost pod) -->
      <div v-else-if="e.kind === 'checkpoint'" class="compact">
        <Badge tone="warning">{{ e.data.reason || 'checkpoint' }}</Badge>
        <code class="mono">{{ shortSHA(e.data.sha) }}</code>
        <span class="muted">
          <template v-if="e.data.run">run {{ e.data.run }}</template>
          <template v-if="e.data.step"> step {{ e.data.step }}</template>
          · +{{ e.data.added ?? 0 }}/−{{ e.data.deleted ?? 0 }}
        </span>
        <RouterLink v-if="taskLink(e)" class="t" :to="taskLink(e)!">task</RouterLink>
      </div>

      <!-- ci -->
      <div v-else-if="e.kind === 'ci'" class="compact">
        <Badge :tone="ciTone(e.data.state as CIState | undefined)">
          {{ e.data.state ? (ciLabel[e.data.state as CIState] ?? e.data.state) : 'CI' }}
        </Badge>
        <span v-if="e.data.failedJobs?.length" class="jobs">
          <Badge v-for="j in e.data.failedJobs" :key="j" tone="danger">{{ j }}</Badge>
        </span>
        <Badge v-if="e.data.round" tone="progress">fix round {{ e.data.round }}</Badge>
        <ExtLink v-if="e.data.pipelineURL" :href="e.data.pipelineURL" class="t">
          pipeline{{ e.data.pipelineID ? ` #${e.data.pipelineID}` : '' }}
        </ExtLink>
      </div>

      <!-- review -->
      <div v-else-if="e.kind === 'review'" class="card">
        <div class="line">
          <Badge :tone="(e.data.blocking ?? 0) > 0 ? 'danger' : 'success'">
            {{ e.data.blocking ?? 0 }} blocker{{ (e.data.blocking ?? 0) === 1 ? '' : 's' }}
          </Badge>
          <Badge v-if="e.data.reviewErrors" tone="warning">{{ e.data.reviewErrors }} errors</Badge>
        </div>
      </div>

      <!-- needs -->
      <div v-else-if="e.kind === 'needs'" class="card warn">
        <p><Markdown mode="inline" :text="e.data.message || e.data.reason || e.summary" /></p>
        <RouterLink v-if="taskLink(e)" class="btn" :to="taskLink(e)!">Open task</RouterLink>
      </div>

      <!-- merged / closed -->
      <div v-else-if="e.kind === 'merged' || e.kind === 'closed'" class="compact">
        <Badge :tone="e.kind === 'merged' ? 'success' : 'muted'">{{ e.kind }}</Badge>
        <ExtLink v-if="e.data.mergeRequestURL" :href="e.data.mergeRequestURL" class="t">MR</ExtLink>
      </div>

      <!-- autopilot -->
      <div v-else-if="e.kind === 'autopilot'" class="compact">
        <Badge tone="progress">{{ e.data.phase || 'autopilot' }}</Badge>
        <span v-if="e.data.reason" class="muted"><Markdown mode="inline" :text="e.data.reason" /></span>
      </div>

      <!-- proposal -->
      <div v-else-if="e.kind === 'proposal'" class="card pilot">
        <Markdown v-if="e.data.body" :text="e.data.body" />
        <p v-else>{{ e.summary }}</p>
        <div v-if="e.data.proposalState === 'open' || !e.data.proposalState" class="btns">
          <button
            type="button"
            class="btn primary"
            :disabled="!canSteer || posting"
            @click="decide(true)"
          >
            yes P{{ e.data.proposalID }}
          </button>
          <button type="button" class="btn" :disabled="!canSteer || posting" @click="decide(false)">
            no P{{ e.data.proposalID }}
          </button>
        </div>
        <p v-if="sendError" class="err">{{ sendError }}</p>
      </div>

      <!-- retrospective: links to the milestone's Retrospective tab -->
      <div v-else-if="e.kind === 'retrospective'" class="compact">
        <Badge tone="info">retrospective</Badge>
        <RouterLink v-if="e.milestoneIID" :to="retroTo(e)">Open the retrospective</RouterLink>
        <span v-if="e.data.followUps" class="muted">
          {{ e.data.followUps }} follow-up{{ e.data.followUps === 1 ? '' : 's' }}
        </span>
      </div>

      <!-- release.decided / started / ended: why and reason are untrusted, as text -->
      <div v-else-if="isRelease" class="compact">
        <Badge v-if="e.kind === 'release.decided'" tone="neutral">decided</Badge>
        <Badge v-else-if="e.kind === 'release.started'" tone="progress">
          started{{ (e.data.attempt ?? 0) > 1 ? ` · attempt ${e.data.attempt}` : '' }}
        </Badge>
        <Badge v-else-if="releaseBadge" :tone="releaseBadge.tone">{{ releaseBadge.label }}</Badge>
        <span v-if="releaseEventText(e)" class="why"><Markdown mode="inline" :text="releaseEventText(e)" /></span>
        <code v-if="e.kind === 'release.started' && e.data.releasedSHA" class="mono">
          {{ shortSHA(e.data.releasedSHA) }}
        </code>
        <span v-if="e.kind === 'release.started' && releaseEventIssues(e)" class="muted">
          contains {{ releaseEventIssues(e) }}
        </span>
        <ExtLink v-if="e.kind === 'release.started' && e.data.pipelineURL" :href="e.data.pipelineURL" class="t">
          pipeline{{ e.data.pipelineID ? ` #${e.data.pipelineID}` : '' }}
        </ExtLink>
        <ExtLink v-if="e.kind === 'release.ended' && e.data.releaseURL" :href="e.data.releaseURL" class="t">
          release{{ e.data.version ? ` ${e.data.version}` : '' }}
        </ExtLink>
      </div>

      <!-- prompt.posted: the compact card while status still has the prompt -->
      <PromptCardCompact
        v-else-if="prompt"
        :namespace="e.namespace"
        :name="e.project"
        :prompt="prompt"
        :to="promptTo(e)"
      />

      <!-- prompt.posted without its prompt, prompt.answered, prompt.closed: one line -->
      <div v-else-if="isPrompt" class="compact">
        <span>{{ e.summary }}</span>
      </div>

      <!-- Desk chat -->
      <div v-else-if="e.kind === 'chat.user' || e.kind === 'chat.agent'" class="card">
        <div class="line">
          <b>{{ e.data.authorName || e.actor || (e.data.role === 'agent' ? 'agent' : 'user') }}</b>
          <Badge tone="muted">{{ e.data.role || (e.kind === 'chat.agent' ? 'agent' : 'user') }}</Badge>
          <Badge v-if="e.data.kind && e.data.kind !== 'message'" tone="info">{{ e.data.kind }}</Badge>
          <Badge v-if="e.data.state" tone="progress">{{ e.data.state }}</Badge>
        </div>
        <Markdown v-if="e.data.body" :text="e.data.body" />
        <p v-else-if="e.redacted" class="muted">message hidden</p>
        <RouterLink v-if="taskLink(e)" class="t chat-link" :to="taskLink(e)!">Open chat</RouterLink>
      </div>

      <div v-else class="compact">
        <span>{{ e.summary }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ev {
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr);
  gap: 10px;
  margin-bottom: 14px;
}
.av {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 14px;
  font-weight: 700;
  background: var(--surface-3);
  color: var(--text-2);
}
.meta {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12.5px;
  color: var(--text-3);
  margin-bottom: 4px;
}
.meta b {
  color: var(--text);
  font-weight: 600;
  font-size: 13.5px;
}
.ctx {
  color: var(--text-2);
}
.compact {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 8px 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  font-size: 13.5px;
}
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  padding: 12px 14px;
}
.card.pilot {
  background: var(--pilot-bg);
  border-color: var(--pilot-border);
}
.card.warn,
.ev.warn .card {
  background: var(--tone-warning-bg);
  border-color: color-mix(in srgb, var(--tone-warning-fg) 25%, var(--border));
}
.card p {
  margin: 0 0 8px;
}
.line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.why {
  min-width: 0;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.jobs {
  display: inline-flex;
  gap: 4px;
  flex-wrap: wrap;
}
.t {
  margin-left: auto;
  font-size: 12px;
}
.btns {
  display: flex;
  gap: 6px;
  margin-top: 10px;
  flex-wrap: wrap;
}
.btn {
  border: 1px solid var(--border-strong);
  background: var(--surface);
  padding: 4px 11px;
  border-radius: 7px;
  font: inherit;
  font-size: 12.5px;
  font-weight: 600;
  cursor: pointer;
  color: var(--text);
  text-decoration: none;
}
.btn:hover {
  background: var(--surface-2);
  text-decoration: none;
}
.btn.primary {
  background: var(--text);
  color: var(--bg);
  border-color: var(--text);
}
.btn:disabled {
  opacity: 0.5;
  cursor: default;
}
.err {
  color: var(--tone-danger-fg);
  font-size: 13px;
  margin-top: 6px;
}
.muted {
  color: var(--text-2);
}
.chat-link {
  display: inline-block;
  margin: 8px 0 0;
  margin-left: 0;
}
</style>
