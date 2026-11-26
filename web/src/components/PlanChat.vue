<script setup lang="ts">
import { errorMessage } from '../format'
import UAlert from '@nuxt/ui/components/Alert.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import { alertColor } from '../badgeTone'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { chatPostError } from '../api'
import { bodyTooLong, uploadAll, withAttachments, type PlanUploads } from '../attachments'
import { getGitLabStatus, getMilestones, getPlanChat, LoginRequiredError, postPlanChat } from '../client'
import { uploadPlanFile } from '../planUpload'
import type { MilestoneRow, PlanDraftView } from '../generated/views'
import type { RefItemSource } from '../stream'
import * as cs from '../chatState'
import { chatMessages } from '../chatView'
import { keepThroughSignIn } from '../signInDraft'
import type { AgentPlanPhase, PlanDecision } from '../generated/api'
import type { ChatMessage } from '../types'
import Badge from './Badge.vue'
import DeskChat from '../ui/DeskChat.vue'
import ChatTurn from './ChatTurn.vue'
import Composer, { type ComposerSend } from './Composer.vue'

// PlanChat is a plan's chat (TaskChat without commands): the newest 100
// messages, polled every 5 s, and a composer with Interrupt while a turn runs.
const props = defineProps<{
  namespace: string
  name: string
  plan: string
  canPost: boolean
  phase?: AgentPlanPhase
  // The budget cap (PlanView.budgetUSD) and spend, for the "budget used" stop.
  spendUSD?: string
  budgetUSD?: string
  decisions?: PlanDecision[]
  // The plan's drafts, offered by the composer's % menu with the milestones.
  drafts?: PlanDraftView[]
  // How messages show their attachments (Plan.vue), once loaded.
  uploads?: PlanUploads
}>()

// The project's milestones for the % menu, loaded once when the composer
// first has the focus; a failed load leaves the drafts only.
const milestones = ref<MilestoneRow[]>([])
let milestonesFor = ''
async function loadMilestones() {
  const project = `${props.namespace}/${props.name}`
  if (milestonesFor === project) return
  milestonesFor = project
  try {
    const list = await getMilestones({ state: 'all', project, limit: 200 })
    if (milestonesFor === project) milestones.value = list.milestones
  } catch {
    if (milestonesFor === project) milestonesFor = ''
  }
}
const references = computed<RefItemSource>(() => ({ milestones: milestones.value, drafts: props.drafts ?? [] }))

const messages = ref<ChatMessage[]>([])
const error = ref('')
const grouped = computed(() => cs.chatTurns(messages.value))
const shown = computed(() => chatMessages(grouped.value.chronological))
const waiting = computed(() => {
  const u = grouped.value.newestUser
  const pending = !!u && cs.agentTurnPending(u.kind, u.state, grouped.value.repliesByParent.has(u.id))
  return cs.planChatWaiting(pending, u?.state === 'delivered', props.phase)
})
const blocked = computed(() => cs.planChatBlocked(props.phase, props.spendUSD, props.budgetUSD))

const key = () => `${props.namespace}/${props.name}/${props.plan}`
let seq = 0
async function load() {
  const mine = ++seq
  try {
    const page = await getPlanChat(props.namespace, props.name, props.plan, { limit: 100 })
    if (mine !== seq) return
    messages.value = page.messages
    error.value = ''
  } catch (e) {
    if (mine === seq && !(e instanceof LoginRequiredError)) error.value = errorMessage(e)
  }
}

// The draft and its clientKey stay in sessionStorage until a 2xx (TaskChat).
const body = ref('')
const sending = ref(false)
const sendError = ref('')
// The message's files (chips) until a post succeeds, and the link to
// connect GitLab after an upload answered 409.
const files = ref<File[]>([])
const connectUrl = ref('')
keepThroughSignIn(() => `plan-chat:${key()}`, body)
const draftKey = () => cs.planChatDraftKey(props.namespace, props.name, props.plan)
// Text kept through a sign-in (newer) wins over a failed send's draft.
function restore(switched: boolean) {
  if (switched) [messages.value, body.value, sendError.value, files.value, connectUrl.value] = [[], '', '', [], '']
  body.value ||= cs.readChatDraft(draftKey())?.body ?? ''
  void load()
}

// showConnect offers the link to connect GitLab (as the stream's note
// composer builds it), when the user has none connected.
async function showConnect() {
  try {
    const st = await getGitLabStatus()
    if (!st.connected && st.login) connectUrl.value = `${st.login}?return=${encodeURIComponent(window.location.pathname + window.location.search)}`
  } catch {
    // The error line already says to connect GitLab.
  }
}

// postBody uploads the files one by one and answers the message with
// their lines, or sets the error and answers null: nothing is posted, and
// the text and the chips stay (Decision 2). A text over the limit uploads
// nothing.
async function postBody(text: string, attached: File[], dk: string, ns: string, name: string): Promise<string | null> {
  if (!attached.length) return text
  if (bodyTooLong(text)) {
    sendError.value = 'The message is larger than 16 KiB.'
    return null
  }
  const up = await uploadAll(attached, (f) => uploadPlanFile(ns, name, f))
  if ('error' in up) {
    if (dk === draftKey()) sendError.value = up.error
    if (up.status === 409 && dk === draftKey()) void showConnect()
    return null
  }
  const full = withAttachments(text, up.markdown)
  if (!bodyTooLong(full)) return full
  if (dk === draftKey()) sendError.value = 'The message with its attachment links is larger than 16 KiB.'
  return null
}

async function onSend(s: ComposerSend) {
  const attached = s.kind === 'message' ? (s.files ?? []) : []
  const text = cs.chatPostBody(s.kind, s.body)
  if (sending.value || (!text && !attached.length)) return
  const dk = draftKey()
  const old = cs.readChatDraft(dk)
  const clientKey = old?.kind === s.kind ? old.clientKey : crypto.randomUUID()
  cs.writeChatDraft(dk, { body: s.body, clientKey, kind: s.kind })
  sending.value = true
  sendError.value = ''
  connectUrl.value = ''
  // The plan the message was written on, should another open during the uploads.
  const { namespace, name, plan } = props
  try {
    const posted = await postBody(text, attached, dk, namespace, name)
    if (posted === null) return
    await postPlanChat(namespace, name, plan, { body: posted, clientKey, kind: s.kind })
    cs.writeChatDraft(dk, null)
    if (dk !== draftKey()) return
    body.value = ''
    // Files attached while the uploads ran stay for the next message.
    files.value = files.value.filter((f) => !attached.includes(f))
    void load()
  } catch (e) {
    if (dk === draftKey()) sendError.value = chatPostError(e)
  } finally {
    sending.value = false
  }
}

let timer: number | undefined
watch(key, () => restore(true))
onMounted(() => {
  restore(false)
  timer = window.setInterval(() => {
    if (document.visibilityState !== 'hidden') void load()
  }, 5000)
})
onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <div class="card flex h-[min(72dvh,680px)] min-h-[360px] flex-col gap-2 overflow-clip p-3" data-plan-chat>
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" :description="error" />
    <DeskChat label="Plan chat" replier="Planner" :messages="shown" :waiting="!!waiting" empty="No messages yet.">
      <template #content="{ message }">
        <ChatTurn v-if="message.metadata.chat" :message="message.metadata.chat" :decisions="decisions" :attachments="uploads ?? { namespace, name }" />
      </template>
      <template #bar>
        <div v-if="waiting" class="flex items-center" data-chat-status>
          <Badge tone="progress" dot pulse>{{ waiting.label }}</Badge>
        </div>
      </template>
      <template #prompt>
        <Composer
          v-if="canPost"
          v-model="body"
          v-model:attachments="files"
          mode="chat"
          placeholder="Tell the agent what the milestone should do…"
          hint="Enter+Ctrl/Cmd to send"
          :blocked="blocked"
          :busy="sending"
          :disabled="sending"
          :error="sendError"
          :can-interrupt="!!waiting?.canInterrupt"
          :references="references"
          @focusin="loadMilestones"
          @send="onSend"
        >
          <template #controls>
            <div v-if="connectUrl" class="mb-2 flex justify-end">
              <UButton :href="connectUrl" color="primary" size="sm" label="Connect GitLab" />
            </div>
          </template>
        </Composer>
      </template>
    </DeskChat>
  </div>
</template>
