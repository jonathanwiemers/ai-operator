<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, type RouteLocationRaw } from 'vue-router'
import { usePromptAnswer } from '../promptAnswer'
import { keepThroughSignIn } from '../signInDraft'
import {
  canAnswerText,
  closedOptions,
  promptAnchor,
  promptOpen,
  promptOptions,
  promptStatus,
  readOnlyText,
  recommendedOption,
  retryWarning,
  shortStatus,
  textBox,
} from '../promptCard'
import type { Facts } from '../generated/problem'
import type { AnswerEvent } from '../problemThread'
import type { PromptView } from '../types'
import Truncated from '../ui/Truncated.vue'
import { announce } from '../ui/useAnnounce'
import Badge from './Badge.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'

// PromptCard is one question to the owners (docs/contributing/web-ui.md, "Prompt card").
//
// In full (the task page, the Pilot tab, pause cards): the question, its
// context, the options (recommended first, a detail line each), a text box
// when it takes free text, and who answered it. One click on an option, or
// the text box, sends an answer command to the prompt's target task: the
// buttons and the text box give way to "✓ <label> · sending…" at once, come
// back with the error when the answer fails, and stay away ("· sent") once
// it went through. A closed card shows only the chosen option with its
// actions, the outcome and who closed it why; the other options fold.
//
// compact (compact pause cards): the question on one line, the recommended
// option as a button and "more…" to the full card (`to`).
//
// The question, context, options and answers are untrusted: text, or
// markdown without HTML.
const props = defineProps<{
  namespace: string
  name: string
  prompt: PromptView
  compact?: boolean
  // to: where "more…" of the compact card leads.
  to?: RouteLocationRaw
  // unanchored: a copy of a card shown elsewhere with the anchor.
  unanchored?: boolean
  // facts: the problem's facts (the problem thread); a retry option warns when
  // they show uncommitted lines (retryWarning).
  facts?: Facts
}>()

const anchor = computed(() => promptAnchor(props.prompt.id))
const open = computed(() => promptOpen(props.prompt))
const options = computed(() => promptOptions(props.prompt))
const closed = computed(() => closedOptions(props.prompt))
const recommended = computed(() => recommendedOption(props.prompt))
const status = computed(() => promptStatus(props.prompt))
const badge = computed(() => shortStatus(props.prompt))
const readOnly = computed(() => readOnlyText(props.prompt))
const withText = computed(() => canAnswerText(props.prompt))
const box = computed(() => textBox(props.prompt))
const clickable = computed(() => open.value && props.prompt.canAnswer)

// answer: the answer on its way and how it went (the problem thread shows
// it as the owner's message, Decision 8).
const emit = defineEmits<{ answer: [e: AnswerEvent] }>()

const { sending, error, sent, answer, busy, send } = usePromptAnswer(
  () => ({ namespace: props.namespace, name: props.name }),
  () => props.prompt,
)

// picked: the label of the answer on its way; settling: from the click
// until the answer failed (the buttons come back then).
const picked = ref('')
const settling = computed(() => !!sending.value || (!!sent.value && answer.value.state !== 'failed'))
const settled = computed(() => !sending.value && answer.value.state === 'answered')
const failure = computed(() =>
  error.value || (sent.value && answer.value.state === 'failed' ? `${sent.value.label}: ${answer.value.reason}` : ''),
)

// The answer takes away the option or text box that had focus: focus goes
// to the "✓ … sending…" line, so a keyboard or screen reader user stays in
// the card, and back to the first option when the answer fails. A failure
// is said at once (useAnnounce.ts); the error line shows it.
const statusLine = ref<HTMLElement | null>(null)
function refocus(to: () => HTMLElement | null | undefined) {
  void nextTick(() => to()?.focus({ preventScroll: true }))
}
watch(failure, (e) => {
  if (e) announce(e, { assertive: true })
})

async function answerWith(label: string, choice: { option: string } | { text: string }): Promise<boolean> {
  if (busy.value) return false
  const held = !!el.value?.contains(document.activeElement)
  picked.value = label
  const shown = { id: crypto.randomUUID(), label, task: props.prompt.target.task }
  emit('answer', { ...shown, send: 'sending' })
  const sending = send(choice)
  if (held) refocus(() => statusLine.value)
  const ok = await sending
  if (held && !ok) {
    const back = 'text' in choice ? 'textarea' : 'button'
    refocus(() => (el.value?.contains(document.activeElement) ? null : el.value?.querySelector<HTMLElement>(back)))
  }
  emit('answer', ok ? { ...shown, send: 'sent' } : { ...shown, send: 'failed', error: error.value })
  return ok
}

function pick(o: { id: string; label: string }) {
  void answerWith(o.label, { option: o.id })
}

const text = ref('')
keepThroughSignIn(() => `prompt:${props.namespace}/${props.name}/${props.prompt.id}`, text)
async function sendText() {
  if (await answerWith(text.value.trim() || 'your answer', { text: text.value })) text.value = ''
}

// A Desk link from GitLab ends in #prompt-<id>: scroll to the full card
// once it is there.
const route = useRoute()
const el = ref<HTMLElement | null>(null)
function scrollIfLinked() {
  if (!props.compact && !props.unanchored && route.hash === `#${anchor.value}`) {
    void nextTick(() => el.value?.scrollIntoView({ block: 'start' }))
  }
}
onMounted(scrollIfLinked)
watch(() => route.hash, scrollIfLinked)

// replacedTo: the card of the prompt that replaced this one. A hash link
// without ?tab=: milestoneTabFromRoute opens the tab that holds the card.
const replacedTo = computed(() =>
  status.value.replacedBy ? { hash: `#${promptAnchor(status.value.replacedBy)}` } : undefined,
)

const small = 'text-[13px]'
const err = 'm-0 text-[13px] text-(--tone-danger-fg)'
</script>

<template>
  <section
    :id="compact || unanchored ? undefined : anchor"
    ref="el"
    :data-prompt="prompt.id"
    class="card flex min-w-0 scroll-mt-3 flex-col border-l-3 [&>p]:m-0"
    :class="[
      open ? 'border-l-(--tone-warning-fg)' : 'border-l-(--border-strong)',
      compact ? 'gap-1.5 px-3.5 py-2.5' : 'gap-2 px-3.5 py-3',
    ]"
  >
    <template v-if="compact">
      <div class="flex min-w-0 items-baseline gap-2">
        <code>{{ prompt.id }}</code>
        <Truncated class="min-w-0 flex-1 font-semibold">
          <Markdown mode="inline" :text="prompt.question" />
        </Truncated>
      </div>
      <div class="flex min-w-0 items-center gap-2">
        <span v-if="open && settling" ref="statusLine" role="status" tabindex="-1" class="min-w-0 flex-1 truncate text-(--text-2) focus:outline-none" :class="small">
          ✓ {{ picked }} · {{ settled ? 'sent' : 'sending…' }}
        </span>
        <UButton
          v-else-if="clickable && recommended"
          color="primary"
          class="max-w-full min-w-0"
          :ui="{ label: 'truncate' }"
          :label="recommended.label"
          @click="pick(recommended)"
        />
        <span v-else-if="!open" class="min-w-0 flex-1 truncate text-(--text-2)" :class="small">{{ status.text }}</span>
        <span v-else-if="readOnly" class="min-w-0 flex-1 truncate text-(--text-3)" :class="small">{{ readOnly }}</span>
        <span class="flex-1" />
        <RouterLink v-if="to" :to="to" class="whitespace-nowrap" :class="small">more…</RouterLink>
      </div>
    </template>

    <template v-else>
      <div class="flex flex-wrap items-center gap-2 text-[12.5px]">
        <code>{{ prompt.id }}</code>
        <Badge :tone="status.tone">{{ badge }}</Badge>
        <span v-if="prompt.issueIID" class="text-(--text-3)">#{{ prompt.issueIID }}</span>
        <span v-if="prompt.mrIID" class="text-(--text-3)">!{{ prompt.mrIID }}</span>
        <span class="flex-1" />
        <span class="text-(--text-3)"><RelTime :time="prompt.postedAt" /></span>
      </div>
      <Markdown class="font-semibold wrap-anywhere" :text="prompt.question" />
      <Markdown
        v-if="prompt.context && open"
        class="max-h-[220px] overflow-y-auto rounded-(--radius-sm) bg-(--surface-2) px-2 py-1.5"
        :class="small"
        :text="prompt.context"
      />

      <template v-if="open && settling">
        <p ref="statusLine" role="status" tabindex="-1" class="font-[650] wrap-anywhere focus:outline-none">✓ {{ picked }} · {{ settled ? 'sent' : 'sending…' }}</p>
        <Markdown
          v-if="settled && answer.state === 'answered' && !answer.redacted"
          :class="small"
          :text="answer.text"
        />
      </template>

      <template v-else-if="open">
        <ul class="m-0 grid list-none gap-2 p-0">
          <li v-for="o in options" :key="o.id" class="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
            <UButton
              v-if="clickable"
              :color="o.recommended ? 'primary' : 'neutral'"
              :variant="o.recommended ? 'solid' : 'outline'"
              class="max-w-full text-left font-[550] wrap-anywhere"
              @click="pick(o)"
            >
              {{ o.label }}
            </UButton>
            <span v-else class="max-w-full rounded-(--radius-sm) border border-(--border-strong) px-2.5 py-1.5 font-medium wrap-anywhere">
              {{ o.label }}
            </span>
            <span v-for="(t, i) in o.tags" :key="t" class="text-[11.5px] text-(--text-3)">{{ i ? `· ${t}` : t }}</span>
            <p v-if="o.consequence" class="m-0 basis-full text-[12.5px] text-(--text-2) wrap-anywhere" data-consequence>{{ o.consequence }}</p>
            <p v-if="retryWarning(o, facts)" class="m-0 basis-full text-[12.5px] font-medium text-(--tone-warning-fg) wrap-anywhere" data-retry-warning>
              {{ retryWarning(o, facts) }}
            </p>
            <Markdown v-if="o.detail" class="basis-full text-[12.5px] text-(--text-2) wrap-anywhere" :text="o.detail" />
            <p v-for="(a, i) in o.actions" :key="i" class="m-0 basis-full text-[12.5px] text-(--text-3) wrap-anywhere">
              → {{ a }}
            </p>
          </li>
        </ul>

        <form v-if="withText" class="grid gap-1.5" @submit.prevent="sendText">
          <UTextarea
            v-model="text"
            :rows="2"
            autoresize
            :maxrows="10"
            :placeholder="box.placeholder"
            :aria-label="`Answer ${prompt.id} in your words`"
            :ui="{ root: 'w-full', base: 'text-base/5 tablet:text-sm/5' }"
          />
          <div class="flex flex-wrap items-center gap-2">
            <span class="min-w-0 flex-[1_1_160px] text-xs text-(--text-3)">{{ box.hint }}</span>
            <UButton type="submit" color="primary" class="justify-center" label="Send" :disabled="!text.trim()" />
          </div>
        </form>

        <p v-if="readOnly" class="text-(--text-3)">{{ readOnly }}</p>
      </template>

      <!-- closed: the choice with its actions, the outcome, the label; the rest folds -->
      <template v-else>
        <ul v-if="closed.shown.length" class="m-0 grid list-none gap-2 p-0">
          <li v-for="o in closed.shown" :key="o.id" class="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
            <span class="max-w-full rounded-(--radius-sm) border border-(--tone-success-fg) px-2.5 py-1.5 font-[650] wrap-anywhere">
              <span aria-label="chosen">✓ </span>{{ o.label }}
            </span>
            <p v-for="(a, i) in o.actions" :key="i" class="m-0 basis-full text-[12.5px] text-(--text-3) wrap-anywhere">
              → {{ a }}
            </p>
          </li>
        </ul>
        <Markdown v-if="prompt.answer?.text && prompt.state === 'answered'" :class="small" :text="prompt.answer.text" />
        <Markdown v-if="prompt.answer?.outcome" :class="small" :text="prompt.answer.outcome" />
      </template>

      <p :class="[small, open && 'text-(--text-2)']">
        <template v-if="replacedTo">
          Replaced by <RouterLink :to="replacedTo">{{ status.replacedBy }}</RouterLink>
        </template>
        <template v-else>{{ status.text }}</template>
        <template v-if="!open && status.at"> · <RelTime :time="status.at" /></template>
      </p>

      <details v-if="!open && closed.folded.length" :class="small">
        <summary class="min-h-11 cursor-pointer py-3 text-(--text-2) tablet:min-h-0 tablet:py-0">{{ closed.fold }} ({{ closed.folded.length }})</summary>
        <ul class="m-0 mt-2 grid list-none gap-2 p-0">
          <li v-for="o in closed.folded" :key="o.id" class="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
            <span class="max-w-full rounded-(--radius-sm) border border-(--border-strong) px-2.5 py-1.5 font-medium wrap-anywhere">
              {{ o.label }}
            </span>
            <span v-for="(t, i) in o.tags" :key="t" class="text-[11.5px] text-(--text-3)">{{ i ? `· ${t}` : t }}</span>
            <Markdown v-if="o.detail" class="basis-full text-[12.5px] text-(--text-2) wrap-anywhere" :text="o.detail" />
            <p v-for="(a, i) in o.actions" :key="i" class="m-0 basis-full text-[12.5px] text-(--text-3) wrap-anywhere">
              → {{ a }}
            </p>
          </li>
        </ul>
      </details>
    </template>
    <p v-if="failure" :class="err">{{ failure }}</p>
  </section>
</template>
