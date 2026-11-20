<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UFormField from '@nuxt/ui/components/FormField.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'
import { noteTargetLabel, slashOpens, slashPick, type NoteTarget, type SlashSuggestion } from '../stream'
import { announce } from '../ui/useAnnounce'
import { useKeyboardInset } from '../ui/useKeyboardInset'

// Composer is the one text box of the Desk (docs/contributing/web-ui.md, "Composer"): the
// task's and a plan's chat (mode chat, TaskChat and PlanChat) and the stream's notes (mode note,
// Stream and the milestone's Pilot tab). It holds the text, the send and
// interrupt buttons, the note's target and command menus, the error line
// and Ctrl/Cmd+Enter; the caller owns the draft, the busy and error state
// and the API call, and puts its own controls (the chat's commands) into
// the controls slot above the box.
export interface ComposerSend {
  kind: 'message' | 'interrupt'
  body: string
  target: NoteTarget | null
}

const props = defineProps<{
  mode: 'chat' | 'note'
  placeholder: string
  hint: string
  // busy: a send is on its way (Send shows …, Send and Interrupt wait).
  busy?: boolean
  // disabled: the text, Send, Interrupt and the target are off.
  disabled?: boolean
  error?: string
  // chat: Interrupt next to Send.
  canInterrupt?: boolean
  // note: where to post (v-model:target) and the commands offered on "/".
  targets?: NoteTarget[]
  suggestions?: SlashSuggestion[]
  // note: the reason the composer cannot be used at all, shown instead of
  // the box, with a link to connect GitLab when there is one.
  blocked?: string
  connectUrl?: string
}>()
const body = defineModel<string>({ required: true })
const target = defineModel<NoteTarget | null>('target', { default: null })
const emit = defineEmits<{ send: [payload: ComposerSend] }>()

useKeyboardInset()

// A send error is said at once (useAnnounce.ts); the error line shows it.
watch(
  () => props.error,
  (e) => {
    if (e) announce(e, { assertive: true })
  },
)

const targetOpen = ref(false)
const slashOpen = ref(false)
const box = useTemplateRef<HTMLElement>('box')

function send(kind: ComposerSend['kind'] = 'message') {
  if (props.busy || props.disabled) return
  if (kind === 'message' && !body.value.trim()) return
  emit('send', { kind, body: body.value, target: target.value })
}

function onInput() {
  if (!props.suggestions?.length) return
  slashOpen.value = slashOpens(body.value)
  if (slashOpen.value) targetOpen.value = false
}

function pickSlash(cmd: string) {
  body.value = slashPick(cmd)
  slashOpen.value = false
}

function pickTarget(t: NoteTarget) {
  target.value = t
  targetOpen.value = false
}

// A click outside the box closes the menus.
function onDocClick(ev: MouseEvent) {
  if (!box.value?.contains(ev.target as Node | null)) {
    targetOpen.value = false
    slashOpen.value = false
  }
}
onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

const menu =
  'absolute top-[calc(100%+6px)] left-2.5 z-20 w-[min(340px,calc(100%-20px))] rounded-[10px] border border-(--border-strong) bg-(--surface) p-1.5 shadow-lg'
const option = 'flex w-full items-baseline gap-2.5 rounded-[7px] px-2 py-1.5 text-left font-normal text-(--text)'
</script>

<template>
  <div class="composer" :class="mode">
    <slot name="controls" />
    <div v-if="blocked" class="rounded-(--radius) border border-(--border) bg-(--surface) px-3.5 py-3">
      <p class="mt-0 mb-2.5">{{ blocked }}</p>
      <UButton v-if="connectUrl" :href="connectUrl" color="primary" label="Connect GitLab" />
    </div>
    <!-- The field ties the error line to the text (aria-describedby and
         aria-invalid); its root keeps the composer's own type size. -->
    <UFormField v-else :error="error" :ui="{ root: 'text-[length:inherit]', error: 'mt-1.5 text-[13px] text-(--tone-danger-fg)' }">
      <div
        ref="box"
        class="relative rounded-xl border border-(--border-strong) bg-(--surface) shadow-(--shadow) focus-within:border-(--focus) focus-within:ring-3 focus-within:ring-(--focus)/20"
      >
        <UTextarea
          v-model="body"
          variant="none"
          autoresize
          :rows="mode === 'chat' ? 3 : 2"
          :maxrows="12"
          :placeholder="placeholder"
          :aria-label="mode === 'chat' ? 'Message' : 'Note'"
          :disabled="disabled"
          :ui="{ root: 'flex w-full', base: 'px-3.5 pt-3 pb-1 text-base/5 text-(--text) tablet:text-sm/5' }"
          @input="onInput"
          @keydown.meta.enter="send()"
          @keydown.ctrl.enter="send()"
        />
        <div class="flex items-center gap-2 pt-1.5 pr-2 pb-2" :class="mode === 'note' ? 'pl-2.5' : 'pl-3.5'">
          <UButton
            v-if="targets"
            color="neutral"
            variant="outline"
            size="sm"
            trailing-icon="i-lucide-chevron-down"
            class="max-w-[46%] rounded-full bg-(--pilot-bg) text-[12.5px] font-semibold text-(--text) ring-(--pilot-border)"
            :disabled="disabled"
            :aria-expanded="targetOpen"
            @click.stop="targetOpen = !targetOpen"
          >
            <span class="truncate">{{ noteTargetLabel(target) }}</span>
          </UButton>
          <span class="min-w-0 flex-1 truncate text-xs text-(--text-3)">{{ hint }}</span>
          <UButton
            v-if="canInterrupt"
            color="warning"
            variant="subtle"
            label="Interrupt"
            :disabled="busy || disabled"
            @mousedown.prevent
            @click="send('interrupt')"
          />
          <UButton
            color="primary"
            class="justify-center"
            :label="busy ? '…' : 'Send'"
            :disabled="busy || disabled || !body.trim()"
            @mousedown.prevent
            @click="send()"
          />
        </div>
        <div v-if="targetOpen" :class="menu" @click.stop>
          <div class="px-2 pt-1.5 pb-0.5 text-[11px] tracking-[0.05em] text-(--text-3) uppercase">Send to</div>
          <UButton
            v-for="(t, i) in targets"
            :key="i"
            color="neutral"
            variant="ghost"
            :class="option"
            @click="pickTarget(t)"
          >
            <b class="font-mono text-[12.5px] font-semibold whitespace-nowrap">
              {{ t.type === 'pilot' ? `Pilot · %${t.milestoneIID}` : `#${t.issueIID}` }}
            </b>
            <span class="truncate text-xs text-(--text-3)">{{ t.title }}</span>
          </UButton>
        </div>
        <div v-if="slashOpen" :class="menu" @click.stop>
          <div class="px-2 pt-1.5 pb-0.5 text-[11px] tracking-[0.05em] text-(--text-3) uppercase">Commands</div>
          <UButton
            v-for="s in suggestions"
            :key="s.cmd"
            color="neutral"
            variant="ghost"
            :class="option"
            @click="pickSlash(s.cmd)"
          >
            <b class="font-mono text-[12.5px] font-semibold whitespace-nowrap">{{ s.cmd }}</b>
            <span class="truncate text-xs text-(--text-3)">{{ s.hint }}</span>
          </UButton>
        </div>
      </div>
    </UFormField>
  </div>
</template>

<style scoped>
/* Send and Interrupt keep the focus in the text (@mousedown.prevent): on a
   phone the keyboard stays up, and the bottom bar does not come back and
   move the composer between press and release.

   Where the composer sits, which utilities cannot say per mode and width.
   A note composer stays at the top of its pane, as the stream scrolls. */
.composer.note {
  position: sticky;
  top: 0;
  z-index: 4;
  background: var(--bg);
  padding: 14px 0 10px;
}
.composer.chat {
  margin-top: 10px;
  padding: 0 14px 14px;
}
/* On a phone DeskChat keeps its bottom (and this composer) on screen. */
@media (max-width: 759.98px) {
  .composer.chat {
    margin-top: 0;
  }
}
</style>
