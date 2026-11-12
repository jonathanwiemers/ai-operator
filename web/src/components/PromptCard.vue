<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, type RouteLocationRaw } from 'vue-router'
import { usePromptAnswer } from '../promptAnswer'
import { keepThroughSignIn } from '../signInDraft'
import {
  canAnswerText,
  promptAnchor,
  promptOpen,
  promptOptions,
  promptStatus,
  readOnlyText,
  recommendedOption,
  textBox,
} from '../promptCard'
import type { PromptView } from '../types'
import Truncated from '../ui/Truncated.vue'
import Badge from './Badge.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'

// PromptCard is one question to the owners (docs/ui.md, "Prompt card").
//
// In full (the task page, the Pilot tab, pause cards): the question, its
// context, the options (recommended first, a detail line each), a text box
// when it takes free text, and who answered it. One click on an option, or
// the text box, sends an answer command to the prompt's target task; the
// operator's reply shows under the card.
//
// compact (stream rows, compact pause cards): the question on one line, the
// recommended option as a button and "more…" to the full card (`to`).
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
}>()

const anchor = computed(() => promptAnchor(props.prompt.id))
const open = computed(() => promptOpen(props.prompt))
const options = computed(() => promptOptions(props.prompt))
const recommended = computed(() => recommendedOption(props.prompt))
const status = computed(() => promptStatus(props.prompt))
const readOnly = computed(() => readOnlyText(props.prompt))
const withText = computed(() => canAnswerText(props.prompt))
const box = computed(() => textBox(props.prompt))
const clickable = computed(() => open.value && props.prompt.canAnswer)

const { sending, error, sent, answer, busy, send } = usePromptAnswer(
  () => ({ namespace: props.namespace, name: props.name }),
  () => props.prompt,
)

const text = ref('')
keepThroughSignIn(() => `prompt:${props.namespace}/${props.name}/${props.prompt.id}`, text)
async function sendText() {
  if (await send({ text: text.value })) text.value = ''
}

// A Desk link from GitLab ends in #prompt-<id>: scroll to the full card
// once it is there.
const route = useRoute()
const el = ref<HTMLElement | null>(null)
function scrollIfLinked() {
  if (!props.compact && route.hash === `#${anchor.value}`) {
    void nextTick(() => el.value?.scrollIntoView({ block: 'start' }))
  }
}
onMounted(scrollIfLinked)
watch(() => route.hash, scrollIfLinked)

const small = 'text-[13px]'
const err = 'm-0 text-[13px] text-(--tone-danger-fg)'
</script>

<template>
  <section
    :id="compact ? undefined : anchor"
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
        <UButton
          v-if="clickable && recommended"
          color="primary"
          class="max-w-full min-w-0"
          :ui="{ label: 'truncate' }"
          :label="sending === recommended.id ? '…' : recommended.label"
          :disabled="busy"
          @click="send({ option: recommended.id })"
        />
        <span v-else-if="!open" class="min-w-0 flex-1 truncate text-(--text-2)" :class="small">{{ status.text }}</span>
        <span v-else-if="readOnly" class="min-w-0 flex-1 truncate text-(--text-3)" :class="small">{{ readOnly }}</span>
        <span class="flex-1" />
        <RouterLink v-if="to" :to="to" class="whitespace-nowrap" :class="small">more…</RouterLink>
      </div>
      <p v-if="sent" class="wrap-anywhere" :class="small">
        <strong>{{ sent.label }}:</strong>
        <span v-if="answer.state === 'pending'" class="text-(--text-3)"> sent…</span>
        <span v-else-if="answer.state === 'failed'" class="text-(--tone-danger-fg)"> {{ answer.reason }}</span>
        <span v-else class="text-(--text-2)"> answered</span>
      </p>
    </template>

    <template v-else>
      <div class="flex flex-wrap items-center gap-2 text-[12.5px]">
        <code>{{ prompt.id }}</code>
        <Badge :tone="status.tone">{{ prompt.state || 'open' }}</Badge>
        <span v-if="prompt.issueIID" class="text-(--text-3)">#{{ prompt.issueIID }}</span>
        <span v-if="prompt.mrIID" class="text-(--text-3)">!{{ prompt.mrIID }}</span>
        <span class="flex-1" />
        <span class="text-(--text-3)"><RelTime :time="prompt.postedAt" /></span>
      </div>
      <Markdown class="font-semibold wrap-anywhere" :text="prompt.question" />
      <Markdown
        v-if="prompt.context"
        class="max-h-[220px] overflow-y-auto rounded-(--radius-sm) bg-(--surface-2) px-2 py-1.5"
        :class="small"
        :text="prompt.context"
      />

      <ul class="m-0 grid list-none gap-2 p-0">
        <li v-for="o in options" :key="o.id" class="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
          <UButton
            v-if="clickable"
            :color="o.recommended ? 'primary' : 'neutral'"
            :variant="o.recommended ? 'solid' : 'outline'"
            class="max-w-full text-left font-[550] wrap-anywhere"
            :disabled="busy"
            @click="send({ option: o.id })"
          >
            {{ sending === o.id ? '…' : o.label }}
          </UButton>
          <span
            v-else
            class="max-w-full rounded-(--radius-sm) border px-2.5 py-1.5 wrap-anywhere"
            :class="o.chosen ? 'border-(--tone-success-fg) font-[650]' : 'border-(--border-strong) font-medium'"
          >
            <span v-if="o.chosen" aria-label="chosen">✓ </span>{{ o.label }}
          </span>
          <span v-for="(t, i) in o.tags" :key="t" class="text-[11.5px] text-(--text-3)">{{ i ? `· ${t}` : t }}</span>
          <Markdown
            v-if="o.detail"
            class="basis-full text-[12.5px] text-(--text-2) wrap-anywhere"
            :text="o.detail"
          />
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
          :disabled="busy"
          :ui="{ root: 'w-full', base: 'text-base/5 tablet:text-sm/5' }"
        />
        <div class="flex flex-wrap items-center gap-2">
          <span class="min-w-0 flex-[1_1_160px] text-xs text-(--text-3)">{{ box.hint }}</span>
          <UButton
            type="submit"
            color="primary"
            class="justify-center"
            :label="sending === 'text' ? '…' : 'Send'"
            :disabled="busy || !text.trim()"
          />
        </div>
      </form>

      <p v-if="readOnly" class="text-(--text-3)">{{ readOnly }}</p>
      <p :class="[small, open && 'text-(--text-2)']">
        {{ status.text }}<template v-if="!open && status.at"> · <RelTime :time="status.at" /></template>
      </p>
      <Markdown v-if="prompt.answer?.text && prompt.state === 'answered'" :class="small" :text="prompt.answer.text" />
      <Markdown v-if="prompt.answer?.outcome" :class="small" :text="prompt.answer.outcome" />

      <div v-if="sent" class="wrap-anywhere" :class="small">
        <strong>{{ sent.label }}:</strong>
        <span v-if="answer.state === 'pending'" class="text-(--text-3)"> sent…</span>
        <span v-else-if="answer.state === 'failed'" class="text-(--tone-danger-fg)"> {{ answer.reason }}</span>
        <span v-else-if="answer.redacted" class="text-(--text-2)"> answered</span>
        <Markdown v-else :text="answer.text" />
      </div>
    </template>
    <p v-if="error" :class="err">{{ error }}</p>
  </section>
</template>
