<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed } from 'vue'
import { splitAttachments, uploadHref, type PlanUploads } from '../attachments'
import { chatAuthorLabel as authorLabel, chatStateLabel as stateLabel, commandLabel } from '../chatState'
import type { PlanDecision } from '../generated/api'
import type { ChatMessage } from '../types'
import Badge from './Badge.vue'
import ChatAnswers from './ChatAnswers.vue'
import LocalText from './LocalText.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'

// ChatTurn is one Desk chat message inside DeskChat's bubble (TaskChat,
// PlanChat): who and when, the state of an owner's message with its reason,
// a command as its label, a plan chat's decision answers, else the text.
// With attachments (plan chat), the upload links of the plan's project
// leave the text and show below it: images as thumbnails, other files as
// chips, both opening the Desk route in a new tab; a viewer without a
// connected GitLab gets chips that say to connect it.
const props = defineProps<{
  message: ChatMessage
  // The plan's current decisions, for the answers block (plan chat only).
  decisions?: PlanDecision[]
  attachments?: PlanUploads
}>()
const split = computed(() => {
  const a = props.attachments
  if (!a || !props.message.body) return { text: props.message.body, files: [] }
  return splitAttachments(props.message.body, a.project, (l) => uploadHref(a.namespace, a.name, l))
})
const locked = computed(() => props.attachments?.connected === false)
</script>

<template>
  <div class="flex min-w-0 flex-col gap-1" :data-chat-turn="message.id" :class="{ 'text-(--tone-danger-fg)': message.role === 'system' }">
    <div class="flex flex-wrap items-center gap-2 text-xs text-dimmed">
      <strong>{{ authorLabel(message) }}</strong>
      <span v-if="message.kind === 'interrupt'">interrupt</span>
      <span v-else-if="message.kind === 'command'">command</span>
      <RelTime :time="message.at" />
      <Badge v-if="message.role === 'user' && message.state" :tone="stateLabel(message.state).tone">
        {{ stateLabel(message.state).text }}
      </Badge>
    </div>
    <p v-if="message.redacted && !message.body" class="m-0 text-[13px] text-(--text-3)">message hidden</p>
    <p v-else-if="message.kind === 'command'" class="m-0 font-mono text-[13px]">{{ commandLabel(message.body) }}</p>
    <ChatAnswers
      v-else-if="message.role === 'user' && message.decisions?.length"
      :answers="message.decisions"
      :decisions="decisions ?? []"
      :body="message.body"
    />
    <Markdown v-else-if="split.text" :text="split.text" />
    <div v-if="split.files.length && message.kind !== 'command'" class="flex flex-wrap items-end gap-2" data-chat-attachments>
      <template v-for="f in split.files" :key="f.href">
        <UButton v-if="locked" :href="attachments?.connect" :disabled="!attachments?.connect" color="neutral" variant="outline" size="sm"
          icon="i-lucide-lock" :label="`${f.name}: Connect GitLab to view`" data-attachment-locked />
        <a v-else-if="f.image && attachments?.connected" :href="f.href" target="_blank" rel="noopener" class="block rounded-(--radius-sm)" data-attachment-image>
          <img :src="f.href" :alt="f.name" loading="lazy" class="block max-h-40 min-h-12 min-w-12 max-w-full rounded object-contain-(--radius-sm) border border-(--border)" />
        </a>
        <UButton v-else :href="f.href" target="_blank" rel="noopener" color="neutral" variant="outline" size="sm"
          icon="i-lucide-paperclip" :label="f.name" data-attachment-file />
      </template>
    </div>
    <!-- Every state's reason is text, not the badge's title: a title cannot be read on touch. -->
    <p
      v-if="message.role === 'user' && message.state && message.reason"
      class="m-0 text-[13px]"
      :class="message.state === 'failed' ? 'text-(--tone-danger-fg)' : 'text-(--text-3)'"
    >
      <LocalText :text="message.reason" />
    </p>
  </div>
</template>
