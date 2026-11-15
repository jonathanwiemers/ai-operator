<script setup lang="ts">
// Restart run (R8, Decision 10): owners confirm, the button sends /agent restart through the task chat and shows the operator's answer.
import UButton from '@nuxt/ui/components/Button.vue'
import { defineAsyncComponent, ref, watch } from 'vue'
import { chatPostError } from '../api'
import { getTaskChat, postTaskChat } from '../client'
import { live } from '../live'
import { commandAnswer } from '../pauseCard'
import { useBackClose } from '../ui/useBackClose'

const UModal = defineAsyncComponent(() => import('@nuxt/ui/components/Modal.vue'))
const props = defineProps<{ namespace: string; name: string; task: string; running: boolean }>()
const [open, busy, sent, status] = [ref(false), ref(false), ref(0), ref('')]
useBackClose(open)
async function restart() {
  if (busy.value) return
  [busy.value, open.value, sent.value, status.value] = [true, false, 0, 'Restart sent…']
  const body = JSON.stringify({ cmd: 'restart' })
  await postTaskChat(props.namespace, props.name, props.task, { body, clientKey: crypto.randomUUID(), kind: 'command' })
    .then((r) => ((sent.value = r.id), loadAnswer()), (e) => (status.value = chatPostError(e)))
  busy.value = false
}
async function loadAnswer() {
  const id = sent.value
  const page = id && (await getTaskChat(props.namespace, props.name, props.task, { limit: 50 }).catch(() => null))
  const a = page && sent.value === id ? commandAnswer(page.messages, id) : null
  if (a?.state === 'answered') [status.value, sent.value] = [a.text || 'The operator answered in the chat.', 0]
  if (a?.state === 'failed') [status.value, sent.value] = [a.reason, 0]
}
watch(() => live.version, () => void loadAnswer())
</script>

<template>
  <UButton size="xs" color="neutral" variant="outline" label="Restart run" :disabled="!running || busy" data-restart-run @click="open = true" />
  <span v-if="status" class="min-w-0 text-sm break-words text-(--text-2)" role="status" data-restart-answer>{{ status }}</span>
  <UModal v-model:open="open" title="Restart the run?" :ui="{ overlay: 'z-40', content: 'z-40 tablet:max-w-md' }">
    <template #body>
      <p class="m-0 text-sm">The agent pod is deleted and the run starts again from the workspace (counted as an interruption).</p>
    </template>
    <template #footer>
      <UButton color="neutral" variant="outline" label="Cancel" @click="open = false" />
      <UButton color="error" label="Restart run" :disabled="busy" data-restart-confirm @click="restart" />
    </template>
  </UModal>
</template>
