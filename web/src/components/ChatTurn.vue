<script setup lang="ts">
import { chatAuthorLabel as authorLabel, chatStateLabel as stateLabel, commandLabel } from '../chatState'
import type { PlanDecision } from '../generated/api'
import type { ChatMessage } from '../types'
import Badge from './Badge.vue'
import ChatAnswers from './ChatAnswers.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'

// ChatTurn is one Desk chat message inside DeskChat's bubble (TaskChat,
// PlanChat): who and when, the state of an owner's message with its reason,
// a command as its label, a plan chat's decision answers, else the text.
defineProps<{
  message: ChatMessage
  // The plan's current decisions, for the answers block (plan chat only).
  decisions?: PlanDecision[]
}>()
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
    <Markdown v-else-if="message.body" :text="message.body" />
    <!-- Every state's reason is text, not the badge's title: a title cannot be read on touch. -->
    <p
      v-if="message.role === 'user' && message.state && message.reason"
      class="m-0 text-[13px]"
      :class="message.state === 'failed' ? 'text-(--tone-danger-fg)' : 'text-(--text-3)'"
    >
      {{ message.reason }}
    </p>
  </div>
</template>
