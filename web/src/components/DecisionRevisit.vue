<script setup lang="ts">
import { errorMessage } from '../format'
import UButton from '@nuxt/ui/components/Button.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { computed, onBeforeUnmount, ref } from 'vue'
import { useRoute } from 'vue-router'
import { postTaskNote } from '../client'
import { commentBody, revisitAction } from '../decisions'
import type { DecisionView } from '../generated/views'
import { revisitCommand } from '../milestoneQueue'
import { useCommandSend } from '../useCommandSend'
import CommandStatus from './CommandStatus.vue'
import ExtLink from './ExtLink.vue'

// DecisionRevisit is a card's revisit block (R13): the server's line that
// says what revisiting does (revisit.text), then the action of its mode
// (revisitAction): prompt, the Revisit button, which sends the revisit
// command to the pilot as the queue does and shows the operator's reply;
// comment, a box whose text is commented as `<prefix> revisit <id> <text>`
// on the open issue (the task's notes, in the user's GitLab name); command,
// the command to copy and the GitLab link. None and a user who may not
// revisit get the line only; none links the successor or the follow-up.
const props = defineProps<{ namespace: string; name: string; decision: DecisionView }>()

const route = useRoute()
const r = computed(() => props.decision.revisit)
const action = computed(() => revisitAction(props.decision))

// prompt: the reply names the revisit prompt, in the milestone's Needs you.
const { sending, error, sent, answer, promptID, send } = useCommandSend(() => props)
const revisit = () => send(r.value.task, revisitCommand(props.decision.id), props.decision.id)
const promptTo = computed(() => {
  if (!promptID.value) return undefined
  const { namespace, name, decision } = props
  const params = { namespace, name, iid: String(decision.milestoneIID) }
  return { name: 'stream-milestone' as const, params, query: { tab: 'cockpit' }, hash: `#prompt-${promptID.value}` }
})

// comment: one comment at a time; the box empties when it is posted.
const text = ref('')
const posting = ref(false)
const posted = ref('')
const postError = ref('')
const body = computed(() => commentBody(r.value.command, text.value))
async function comment() {
  const task = r.value.task
  if (!task || !body.value || posting.value) return
  posting.value = true
  posted.value = postError.value = ''
  try {
    await postTaskNote(props.namespace, props.name, task, 'issue', body.value)
    posted.value = `Commented on #${props.decision.issue.iid}.`
    text.value = ''
  } catch (e) {
    postError.value = errorMessage(e)
  } finally {
    posting.value = false
  }
}

// command: the command with a placeholder for the instruction.
const command = computed(() => `${(r.value.command ?? '').trim()} <what you want instead>`)
const copied = ref(false)
let copiedTimer: ReturnType<typeof setTimeout> | undefined
async function copy() {
  try {
    await navigator.clipboard.writeText(command.value)
    copied.value = true
    clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => (copied.value = false), 2000)
  } catch {
    copied.value = false
  }
}
onBeforeUnmount(() => clearTimeout(copiedTimer))

// none: the successor's card, or the open follow-up's issue page.
const followUp = computed(() =>
  r.value.mode === 'none' ? props.decision.revisits?.find((q) => q.followUpIID && q.followUpState === 'opened' && !q.applied) : undefined,
)
const successorTo = computed(() => ({ query: route.query, hash: `#${props.decision.supersededBy}` }))
const followUpTo = computed(() => {
  const { namespace, name, decision } = props
  const params = { namespace, name, iid: String(decision.milestoneIID), issue: String(followUp.value?.followUpIID ?? 0) }
  return { name: 'issue' as const, params }
})

const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
const note = 'text-[13px] text-(--text-3)'
</script>

<template>
  <div v-if="r.text" class="mt-3 flex min-w-0 flex-col gap-2 border-t border-(--border) pt-2.5" data-revisit :data-mode="r.mode || undefined">
    <p class="m-0 flex min-w-0 flex-wrap items-center gap-x-2 text-[12.5px] text-(--text-2)">
      <span class="min-w-0 [overflow-wrap:anywhere]" data-revisit-text>{{ r.text }}</span>
      <template v-if="r.mode === 'none'">
        <RouterLink v-if="decision.supersededBy" :to="successorTo" class="tabular-nums" :class="tap" data-revisit-link>
          {{ decision.supersededBy }}
        </RouterLink>
        <RouterLink v-else-if="followUp" :to="followUpTo" class="tabular-nums" :class="tap" data-revisit-link>
          #{{ followUp.followUpIID }}
        </RouterLink>
      </template>
    </p>

    <template v-if="action === 'button'">
      <div>
        <UButton
          color="neutral"
          variant="outline"
          size="sm"
          class="min-h-11 justify-center tablet:min-h-0"
          :disabled="!!sending"
          :label="sending ? '…' : 'Revisit'"
          :aria-label="`Revisit ${decision.id}`"
          @click="revisit"
        />
      </div>
      <CommandStatus :sent="sent" :answer="answer" :error="error" :prompt="promptID" :prompt-to="promptTo" />
    </template>

    <form v-else-if="action === 'comment'" class="flex min-w-0 flex-col gap-2" @submit.prevent="comment">
      <UTextarea
        v-model="text"
        :rows="2"
        autoresize
        :maxrows="8"
        placeholder="What you want instead"
        :aria-label="`What you want instead of ${decision.id}`"
        class="w-full"
        :ui="{ base: 'text-base tablet:text-sm/5' }"
      />
      <div class="flex flex-wrap items-center gap-2">
        <UButton
          type="submit"
          color="neutral"
          variant="outline"
          size="sm"
          class="min-h-11 justify-center tablet:min-h-0"
          :disabled="posting || !body"
          :label="posting ? '…' : 'Comment'"
        />
        <span v-if="postError" :class="note" role="alert">{{ postError }}</span>
        <span v-else-if="posted" :class="note" role="status">{{ posted }}</span>
      </div>
    </form>

    <div v-else-if="action === 'copy'" class="flex min-w-0 flex-col gap-1.5">
      <div class="flex min-w-0 items-start gap-2">
        <pre
          class="m-0 min-w-0 flex-1 rounded-(--radius-sm) bg-(--surface-2) px-2.5 py-1.5 font-mono text-[12.5px] whitespace-pre-wrap [overflow-wrap:anywhere]"
          data-revisit-command
        >{{ command }}</pre>
        <UButton
          color="neutral"
          variant="outline"
          size="sm"
          class="min-h-11 flex-none justify-center tablet:min-h-0"
          :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
          :label="copied ? 'Copied' : 'Copy'"
          @click="copy"
        />
      </div>
      <ExtLink v-if="decision.url" :href="decision.url" class="text-[12.5px]" :class="tap">Comment on GitLab</ExtLink>
    </div>
  </div>
</template>
