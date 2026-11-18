<script setup lang="ts">
import type { RouteLocationRaw } from 'vue-router'
import { sentLine } from '../milestoneQueue'
import type { CommandAnswer } from '../pauseCard'
import type { Sent } from '../useCommandSend'

// CommandStatus is the line under a queue or a decision card after a chat
// command (useCommandSend): the post's error, the pending line, the failed
// command's reason, or the operator's answer, with a link to the prompt a
// revisit opened. promptTo is that link's target (the milestone's Needs you
// tab); without it the link is #prompt-<id> on the same page.
defineProps<{ sent: Sent | null; answer: CommandAnswer; error: string; prompt: string; promptTo?: RouteLocationRaw }>()

const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
const note = 'text-[13px] text-(--text-3)'
</script>

<template>
  <div v-if="error" :class="note" role="alert">{{ error }}</div>
  <div v-else-if="sent" :class="note" role="status">
    <template v-if="answer.state === 'pending'">{{ sentLine(sent.kind, sent.key, true) }}</template>
    <template v-else-if="answer.state === 'failed'">{{ answer.reason }}</template>
    <template v-else-if="answer.redacted">{{ sentLine(sent.kind, sent.key, false) }}</template>
    <template v-else>
      {{ answer.text }}
      <template v-if="prompt">
        <RouterLink v-if="promptTo" :to="promptTo" :class="tap">Go to {{ prompt }}</RouterLink>
        <a v-else :href="`#prompt-${prompt}`" :class="tap">Go to {{ prompt }}</a>
      </template>
    </template>
  </div>
</template>
