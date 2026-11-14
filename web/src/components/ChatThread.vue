<script setup lang="ts">
import { chatAuthorLabel as authorLabel, chatStateLabel as stateLabel, commandLabel } from '../chatState'
import type { ChatMessage } from '../types'
import Badge from './Badge.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'

// ChatThread renders a Desk chat's turns (chatTurns) with each reply nested
// under its message: TaskChat and PlanChat.
defineProps<{
  turns: ChatMessage[]
  replies: Map<number, ChatMessage>
}>()
</script>

<template>
  <div class="thread">
    <div v-for="msg in turns" :key="msg.id" class="turn">
      <div class="bubble" :class="msg.role">
        <div class="meta">
          <strong>{{ authorLabel(msg) }}</strong>
          <span v-if="msg.kind === 'interrupt'" class="faint">interrupt</span>
          <span v-else-if="msg.kind === 'command'" class="faint">command</span>
          <span class="faint"><RelTime :time="msg.at" /></span>
          <Badge v-if="msg.role === 'user' && msg.state" :tone="stateLabel(msg.state).tone">
            {{ stateLabel(msg.state).text }}
          </Badge>
        </div>
        <p v-if="msg.redacted && !msg.body" class="withheld">message hidden</p>
        <p v-else-if="msg.kind === 'command'" class="cmd">{{ commandLabel(msg.body) }}</p>
        <Markdown v-else-if="msg.body" :text="msg.body" />
        <!-- Every state's reason is text, not the badge's title: a title cannot be read on touch. -->
        <p
          v-if="msg.role === 'user' && msg.state && msg.reason"
          class="m-0 mt-1.5 text-[13px]"
          :class="msg.state === 'failed' ? 'text-(--tone-danger-fg)' : 'text-(--text-3)'"
        >
          {{ msg.reason }}
        </p>
      </div>
      <template v-if="replies.get(msg.id)">
        <div class="bubble reply" :class="replies.get(msg.id)!.role">
          <div class="meta">
            <strong>{{ authorLabel(replies.get(msg.id)!) }}</strong>
            <span class="faint"><RelTime :time="replies.get(msg.id)!.at" /></span>
          </div>
          <p v-if="replies.get(msg.id)!.redacted && !replies.get(msg.id)!.body" class="withheld">message hidden</p>
          <Markdown v-else-if="replies.get(msg.id)!.body" :text="replies.get(msg.id)!.body!" />
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.thread {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px;
  max-height: 520px;
  overflow-y: auto;
}
.turn {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.bubble {
  padding: 10px 12px;
  border-radius: var(--radius);
  background: var(--surface-2);
  border: 1px solid var(--border);
}
.bubble.user {
  background: var(--surface);
}
.bubble.system,
.bubble.reply.system {
  background: var(--tone-danger-bg);
  border-color: color-mix(in srgb, var(--tone-danger-fg) 25%, var(--border));
  color: var(--tone-danger-fg);
}
.bubble.reply {
  margin-left: 18px;
}
.meta {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
  font-size: 13px;
  margin-bottom: 4px;
}
.cmd {
  margin: 0;
  font-family: var(--mono);
  font-size: 13px;
}
.withheld {
  margin: 0;
  font-size: 13px;
  color: var(--text-3);
}
</style>
