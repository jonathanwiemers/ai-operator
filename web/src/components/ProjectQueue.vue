<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { ref, watch } from 'vue'
import { chatPostError } from '../api'
import { getTaskChat, postTaskChat } from '../client'
import type { QueueEntry } from '../generated/views'
import { live } from '../live'
import { commandAnswer, type CommandAnswer } from '../pauseCard'
import { queueActions, queueStateLabel, queueTone, type QueueAction } from '../projectQueue'
import Badge from './Badge.vue'
import CostChip from './CostChip.vue'

// ProjectQueue: a project's open autopilot milestones in run order with
// Run next / Clear mark and Pause / Resume (docs/ui.md). Titles are text.
// Stream reloads the project on live updates; the answer loads here.
const props = defineProps<{ namespace: string; name: string; queue: QueueEntry[]; commands: boolean }>()

const sending = ref('')
const error = ref('')
const sent = ref<{ id: number; task: string } | null>(null)
const answer = ref<CommandAnswer>({ state: 'pending' })

async function send(e: QueueEntry, a: QueueAction) {
  if (sending.value) return
  sending.value = `${e.iid}${a.id}`
  error.value = ''
  try {
    const body = JSON.stringify(a.cmd)
    const r = await postTaskChat(props.namespace, props.name, e.task, { body, clientKey: crypto.randomUUID(), kind: 'command' })
    sent.value = { id: r.id, task: e.task }
    answer.value = { state: 'pending' }
    void loadAnswer()
  } catch (err) {
    error.value = chatPostError(err)
  } finally {
    sending.value = ''
  }
}

async function loadAnswer() {
  const s = sent.value
  if (!s || answer.value.state !== 'pending') return
  try {
    const page = await getTaskChat(props.namespace, props.name, s.task, { limit: 50 })
    if (sent.value === s) answer.value = commandAnswer(page.messages, s.id)
  } catch {
    /* best-effort; the next live update tries again */
  }
}
watch(() => live.version, () => void loadAnswer())
</script>

<template>
  <section v-if="queue.length" class="grid gap-1 text-[13px]" aria-labelledby="project-queue">
    <h2 id="project-queue" class="m-0 text-sm font-semibold">Queue</h2>
    <ol class="m-0 grid list-none gap-0.5 p-0">
      <li v-for="(e, i) in queue" :key="e.iid" class="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md px-1.5 py-1 hover:bg-(--surface-2)" data-queue-entry>
        <span class="w-5 shrink-0 text-right tabular-nums text-(--text-3)">{{ i + 1 }}</span>
        <RouterLink :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(e.iid) } }" class="min-w-0 flex-[1_1_12rem] [overflow-wrap:anywhere]">
          <span class="font-mono text-xs">%{{ e.iid }}</span> {{ e.title }}
        </RouterLink>
        <Badge :tone="queueTone(e)">{{ queueStateLabel(e) }}</Badge>
        <span class="text-xs text-(--text-3) tabular-nums">{{ e.done }}/{{ e.total }} issues</span>
        <CostChip :cost="e" small />
        <span v-if="e.markedBy" class="text-xs text-(--text-3)">marked by {{ e.markedBy }}</span>
        <span v-if="commands" class="ml-auto flex gap-1">
          <UButton v-for="a in queueActions(e, i)" :key="a.id" color="neutral" variant="outline" size="xs" :disabled="!!sending" :label="sending === `${e.iid}${a.id}` ? '…' : a.label" @click="send(e, a)" />
        </span>
      </li>
    </ol>
    <div v-if="error" class="text-(--text-3)" role="alert">{{ error }}</div>
    <div v-else-if="sent" class="text-(--text-3)" role="status">
      {{ answer.state === 'pending' ? 'Sent…' : answer.state === 'failed' ? answer.reason : answer.redacted ? 'Done.' : answer.text }}
    </div>
  </section>
</template>
