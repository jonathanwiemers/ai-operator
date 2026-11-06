<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { chatPostError, getTaskChat, postTaskChat } from '../api'
import { companionOf } from '../companion'
import { live } from '../live'
import { canRevisit, queueRows, queueTone, revisitCommand, revisitPromptID } from '../milestoneQueue'
import { commandAnswer, type CommandAnswer } from '../pauseCard'
import type { MilestoneView } from '../types'
import Badge from './Badge.vue'
import CompanionChip from './CompanionChip.vue'
import ExtLink from './ExtLink.vue'

// QueueList is a milestone's autopilot queue: per issue its state, merge
// request, implement task and the decisions recorded for it. Titles and
// decision titles are untrusted text: rendered as text only.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()

const rows = computed(() =>
  queueRows(props.milestone.autopilot?.items ?? [], props.milestone.spec.status.autopilot?.items),
)

// Revisit: owners send {cmd:'revisit', decision} to the pilot task (the
// target of milestone prompts); the operator's reply names the new prompt.
const pilotTask = computed(() => props.milestone.pilot?.task)
const sending = ref('')
const error = ref('')
const sent = ref<{ id: number; task: string; decision: string } | null>(null)
const answer = ref<CommandAnswer>({ state: 'pending' })
const promptID = computed(() => (answer.value.state === 'answered' ? revisitPromptID(answer.value.text) : ''))

async function revisit(decision: string) {
  const task = pilotTask.value
  if (!task || sending.value) return
  sending.value = decision
  error.value = ''
  try {
    const body = JSON.stringify(revisitCommand(decision))
    const r = await postTaskChat(props.namespace, props.name, task, { body, clientKey: crypto.randomUUID(), kind: 'command' })
    sent.value = { id: r.id, task, decision }
    answer.value = { state: 'pending' }
    void loadAnswer()
  } catch (e) {
    error.value = chatPostError(e)
  } finally {
    sending.value = ''
  }
}

async function loadAnswer() {
  const s = sent.value
  if (!s || answer.value.state !== 'pending') return
  try {
    const page = await getTaskChat(props.namespace, props.name, s.task, { limit: 50 })
    if (sent.value !== s) return
    answer.value = commandAnswer(page.messages, s.id)
  } catch {
    /* best-effort; the next live update tries again */
  }
}
watch(() => live.version, () => void loadAnswer())

// companion: the issue's companion merge request, if it has one.
const companion = (iid: number) => companionOf(props.milestone.companions, iid)

const issueURL = (iid: number) => props.milestone.webURL.replace(/\/-\/milestones\/\d+$/, `/-/issues/${iid}`)
</script>

<template>
  <ul class="queue">
    <li v-for="q in rows" :key="q.iid">
      <div class="top">
        <ExtLink :href="issueURL(q.iid)" class="iid">#{{ q.iid }}</ExtLink>
        <span class="tt" :title="q.title">{{ q.title }}</span>
        <Badge :tone="queueTone(q.state)">{{ q.state.toLowerCase() }}</Badge>
      </div>
      <div v-if="q.mergeRequestIID || q.task || q.decisions.length || q.revisitOf || companion(q.iid)" class="sub">
        <ExtLink v-if="q.mergeRequestURL" :href="q.mergeRequestURL">!{{ q.mergeRequestIID }}</ExtLink>
        <span v-else-if="q.mergeRequestIID">!{{ q.mergeRequestIID }}</span>
        <CompanionChip v-if="companion(q.iid)" :companion="companion(q.iid)!" />
        <RouterLink v-if="q.task" :to="{ name: 'task', params: { namespace, name, task: q.task } }">task</RouterLink>
        <span v-if="q.revisitOf">revisits {{ q.revisitOf }}</span>
        <span v-for="d in q.decisions" :key="d.id" class="decision" :class="{ superseded: d.superseded }" :title="d.title">
          <ExtLink v-if="d.url" :href="d.url"><code>{{ d.id }}</code></ExtLink>
          <code v-else>{{ d.id }}</code>
          <button
            v-if="pilotTask && canRevisit(d, milestone.permissions)"
            type="button"
            class="revisit"
            :disabled="!!sending"
            @click="revisit(d.id)"
          >
            {{ sending === d.id ? '…' : 'Revisit' }}
          </button>
        </span>
      </div>
    </li>
  </ul>
  <div v-if="error" class="empty-sm" role="alert">{{ error }}</div>
  <div v-else-if="sent" class="empty-sm" role="status">
    <template v-if="answer.state === 'pending'">Revisiting {{ sent.decision }}…</template>
    <template v-else-if="answer.state === 'failed'">{{ answer.reason }}</template>
    <template v-else>
      {{ answer.redacted ? `Revisit of ${sent.decision} opened.` : answer.text }}
      <a v-if="promptID" :href="`#prompt-${promptID}`">Go to {{ promptID }}</a>
    </template>
  </div>
  <div v-if="!rows.length" class="empty-sm">The queue is empty.</div>
</template>

<style scoped>
.queue {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 2px;
  font-size: 13px;
}
.queue li {
  padding: 4px 6px;
  border-radius: 6px;
}
.queue li:hover {
  background: var(--surface-2);
}
.top {
  display: grid;
  grid-template-columns: 38px minmax(0, 1fr) auto;
  gap: 6px;
  align-items: center;
}
.iid {
  font-family: var(--mono);
  font-size: 12px;
}
.tt {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sub {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  margin-left: 44px;
  font-size: 12px;
  color: var(--text-3);
}
.decision.superseded {
  opacity: 0.55;
  text-decoration: line-through;
}
.revisit {
  margin-left: 4px;
  padding: 0 6px;
  font-size: 11px;
}
.empty-sm {
  font-size: 13px;
  color: var(--text-3);
}
</style>
