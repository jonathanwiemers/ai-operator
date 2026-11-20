<script setup lang="ts">
// The Desk's one chat layout (docs/contributing/web-ui.md, "DeskChat"): the
// history above, oldest first, and the text box at the bottom. It wraps
// UChatMessages and UChatPrompt; Nuxt UI has the parts but not *Load older*
// with a kept scroll anchor, following new messages only while the reader is
// at the bottom, or operator events shown compact. Messages come from
// chatView.ts (eventMessages, localMessage). The caller owns the draft, the
// API call and the paging. A complete reply and a send error go to the
// announcer (useAnnounce.ts); the log itself is not a live region, so
// streamed text is not read word by word.
import UButton from '@nuxt/ui/components/Button.vue'
import UChatMessages from '@nuxt/ui/components/ChatMessages.vue'
import UChatPrompt from '@nuxt/ui/components/ChatPrompt.vue'
import UChatPromptSubmit from '@nuxt/ui/components/ChatPromptSubmit.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed, nextTick, onMounted, useTemplateRef, watch } from 'vue'
import Markdown from '../components/Markdown.vue'
import RelTime from '../components/RelTime.vue'
import { anchoredTop, atBottom, chatChange, replyAnnouncement, type ChatSnapshot, type DeskChatMessage } from '../chatView'
import { announce } from './useAnnounce'

const props = withDefaults(
  defineProps<{
    messages: DeskChatMessage[]
    // older: there are older messages to load (shows Load older).
    older?: boolean
    loadingOlder?: boolean
    // waiting: an answer is expected (the typing indicator).
    waiting?: boolean
    placeholder?: string
    // noPrompt: no text box (a read-only history); the prompt slot still
    // replaces it.
    noPrompt?: boolean
    disabled?: boolean
    busy?: boolean
    error?: string
    empty?: string
    label?: string
    // replier: who answers here ("Pilot"), for the reply's announcement
    // when a message has no author.
    replier?: string
  }>(),
  { placeholder: 'Write a message…', empty: 'No messages yet', label: 'Conversation' },
)
const draft = defineModel<string>({ default: '' })
const emit = defineEmits<{ submit: [text: string]; older: [] }>()
defineSlots<{
  // content: a message's body instead of its text (e.g. an event card).
  content?(props: { message: DeskChatMessage }): unknown
  // bar: controls above the text box (a prompt's options, ways out).
  bar?(): unknown
  // prompt: replaces the text box.
  prompt?(): unknown
}>()

const scroller = useTemplateRef<HTMLElement>('scroller')
let stuck = true

function onScroll() {
  const el = scroller.value
  if (el) stuck = atBottom(el.scrollTop, el.clientHeight, el.scrollHeight)
}

function toBottom() {
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
}

const ids = computed(() => props.messages.map((m) => m.id))

// Before the DOM changes: remember where the reader is; after: keep the
// anchor on Load older, follow new messages only from the bottom, and
// always show the owner's own message.
watch(
  ids,
  (after, before) => {
    const el = scroller.value
    if (!el) return
    const change = chatChange(before ?? [], after)
    const was = { top: el.scrollTop, height: el.scrollHeight }
    const follow = stuck || props.messages.at(-1)?.metadata.send === 'sending'
    void nextTick(() => {
      if (change === 'prepend') el.scrollTop = anchoredTop(was, el.scrollHeight)
      else if (change !== 'none' && follow) toBottom()
      onScroll()
    })
  },
  { flush: 'pre' },
)

// A reply once it is complete, never per change while it is written.
let seen: ChatSnapshot = { messages: props.messages, waiting: props.waiting }
watch(
  () => ({ messages: props.messages, waiting: props.waiting }),
  (next) => {
    announce(replyAnnouncement(seen, next, props.replier))
    seen = next
  },
)
watch(
  () => props.error,
  (e) => {
    if (e) announce(e, { assertive: true })
  },
)

onMounted(() => {
  toBottom()
  onScroll()
})

function submit() {
  const text = draft.value.trim()
  if (!text || props.busy || props.disabled) return
  emit('submit', text)
}

function text(m: DeskChatMessage): string {
  return m.parts.map((p) => p.text).join('\n\n')
}

// Operator events are compact lines, not bubbles; cards (stream events)
// take the full width.
const shown = computed(() =>
  props.messages.map((m) =>
    m.metadata.card
      ? { ...m, variant: 'naked' as const, ui: { root: 'py-0', container: 'pb-0' } }
      : m.role === 'system'
        ? { ...m, variant: 'naked' as const, ui: { root: 'py-0', content: 'text-sm text-muted' } }
        : m,
  ),
)
</script>

<template>
  <div class="flex min-h-0 flex-1 flex-col" :aria-label="label">
    <!-- tabindex: a keyboard scrolls the thread too. -->
    <div ref="scroller" class="min-h-0 flex-1 overflow-y-auto px-1" role="log" aria-live="off" tabindex="0" @scroll.passive="onScroll">
      <div v-if="older" class="flex justify-center py-2">
        <UButton size="xs" color="neutral" variant="ghost" icon="i-lucide-history" :loading="loadingOlder" @click="emit('older')">
          Load older
        </UButton>
      </div>
      <UEmpty v-if="!messages.length" variant="naked" size="sm" icon="i-lucide-message-square" :title="empty" />
      <UChatMessages
        v-else
        :messages="shown"
        :status="waiting ? 'submitted' : 'ready'"
        :should-scroll-to-bottom="false"
        :assistant="{ side: 'left', variant: 'outline' }"
        :user="{ side: 'right', variant: 'soft' }"
        :ui="{ root: 'gap-2' }"
      >
        <template #content="{ message }">
          <slot name="content" :message="message as DeskChatMessage">
            <div class="flex flex-col gap-1">
              <div
                v-if="message.metadata.author || message.metadata.at || message.metadata.send"
                class="flex flex-wrap items-center gap-2 text-xs text-dimmed"
              >
                <span v-if="message.metadata.author">{{ message.metadata.author }}</span>
                <RelTime v-if="message.metadata.at" :time="message.metadata.at" />
                <span v-if="message.metadata.send === 'sending'">sending…</span>
                <span v-else-if="message.metadata.send === 'sent'">sent</span>
                <span v-else-if="message.metadata.send === 'failed'" class="text-error">
                  not sent{{ message.metadata.error ? `: ${message.metadata.error}` : '' }}
                </span>
              </div>
              <span v-if="message.role === 'system'">
                {{ text(message as DeskChatMessage) }}
                <RouterLink v-if="message.metadata.link" :to="message.metadata.link.to" class="ms-1 underline">{{ message.metadata.link.label }}</RouterLink>
              </span>
              <Markdown v-else :text="text(message as DeskChatMessage)" />
            </div>
          </slot>
        </template>
      </UChatMessages>
    </div>
    <div class="bar flex flex-col gap-2 pt-2 empty:hidden">
      <slot name="bar" />
    </div>
    <div class="prompt pt-2">
      <slot name="prompt">
        <UChatPrompt
          v-if="!noPrompt"
          v-model="draft"
          :placeholder="placeholder"
          :disabled="disabled"
          :autofocus="false"
          variant="subtle"
          @submit="submit"
        >
          <template #footer>
            <span v-if="error" class="text-xs text-error">{{ error }}</span>
            <UChatPromptSubmit class="ms-auto" :status="busy ? 'submitted' : 'ready'" :disabled="disabled || !draft.trim()" />
          </template>
        </UChatPrompt>
      </slot>
    </div>
  </div>
</template>

<style scoped>
/* On a phone the text box stays on screen while the page scrolls: above
   the bottom bar, or, while typing, above the keyboard (--keyboard-inset,
   useKeyboardInset.ts, for iOS; Chrome resizes the page instead). The whole
   chat is its sticky range, so it floats over the history until its end.
   Not while the bar shows something: a question's or a pause's card can be
   taller than the screen, and the text box would cover its buttons. */
@media (max-width: 759.98px) {
  .bar:empty + .prompt {
    position: sticky;
    bottom: max(var(--keyboard-inset, 0px), calc(var(--bottombar, 0px) + env(safe-area-inset-bottom)));
    z-index: 4;
    background: var(--bg);
  }
  .card .bar:empty + .prompt {
    background: var(--surface);
  }
}
</style>
