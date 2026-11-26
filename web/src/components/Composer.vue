<script setup lang="ts">
import { plural } from '../format'
import UButton from '@nuxt/ui/components/Button.vue'
import UFormField from '@nuxt/ui/components/FormField.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, useTemplateRef, watch } from 'vue'
import {
  noteTargetLabel,
  refItems,
  refPick,
  refQuery,
  slashOpens,
  slashPick,
  type NoteTarget,
  type RefItem,
  type RefItemSource,
  type SlashSuggestion,
} from '../stream'
import { announce } from '../ui/useAnnounce'
import { useKeyboardInset } from '../ui/useKeyboardInset'
import { pastedFiles } from '../attachments'
import AttachmentPicker from './AttachmentPicker.vue'

// Composer is the one text box of the Desk (docs/contributing/web-ui.md, "Composer"): the
// task's and a plan's chat (mode chat, TaskChat and PlanChat) and the stream's notes (mode note,
// Stream and the milestone's pilot drawer). It holds the text, the send and
// interrupt buttons, the note's target and command menus, the error line
// and Ctrl/Cmd+Enter; the caller owns the draft, the busy and error state
// and the API call, and puts its own controls (the chat's commands) into
// the controls slot above the box.
export interface ComposerSend {
  kind: 'message' | 'interrupt'
  body: string
  target: NoteTarget | null
  // files: the attached files, when v-model:attachments is bound.
  files?: File[]
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
  // chat: what "%" offers to reference (the plan chat's milestones and
  // drafts); without it "%" opens nothing.
  references?: RefItemSource
}>()
const body = defineModel<string>({ required: true })
const target = defineModel<NoteTarget | null>('target', { default: null })
// attachments: the files of the message (a plan chat's). Unbound
// (undefined), there is no paperclip, paste and drop take no files, and the
// composer is as it was.
const attachments = defineModel<File[]>('attachments')
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

// The text is disabled while a send is on its way, which takes its focus:
// focus comes back after, so a keyboard or screen reader user writes on.
// The watcher runs before the DOM update, while the text still has focus.
let refocus = false
watch(
  () => props.disabled,
  (off) => {
    if (off) refocus = !!box.value?.contains(document.activeElement)
    else if (refocus) {
      refocus = false
      void nextTick(() => box.value?.querySelector('textarea')?.focus())
    }
  },
)

function send(kind: ComposerSend['kind'] = 'message') {
  if (props.busy || props.disabled) return
  if (kind === 'message' && !body.value.trim() && !attachments.value?.length) return
  refAt.value = null
  emit('send', { kind, body: body.value, target: target.value, ...(attachments.value ? { files: attachments.value } : {}) })
}

// Pasted and dropped files go to the picker, only where attachments are
// bound; a paste without files stays a text paste.
const picker = useTemplateRef<InstanceType<typeof AttachmentPicker>>('picker')
let pasted = 0
function onPaste(ev: ClipboardEvent) {
  const list = [...(ev.clipboardData?.files ?? [])]
  if (!attachments.value || props.disabled || !list.length) return
  ev.preventDefault()
  const named = pastedFiles(list, pasted + 1)
  pasted += named.filter((f, i) => f !== list[i]).length
  picker.value?.add(named)
}
const dragging = ref(false)
const hasFiles = (ev: DragEvent) => !!ev.dataTransfer?.types.includes('Files')
function onDragover(ev: DragEvent) {
  if (!attachments.value || props.disabled || !hasFiles(ev)) return
  ev.preventDefault()
  dragging.value = true
}
function onDragleave(ev: DragEvent) {
  if (!box.value?.contains(ev.relatedTarget as Node | null)) dragging.value = false
}
function onDrop(ev: DragEvent) {
  dragging.value = false
  if (!attachments.value || props.disabled || !hasFiles(ev)) return
  ev.preventDefault()
  picker.value?.add([...(ev.dataTransfer?.files ?? [])])
}
const canSend = computed(() => !!body.value.trim() || !!attachments.value?.length)

// The % menu (references), a listbox the text points into with
// aria-activedescendant: refAt is the typed "%query", refActive the option
// the arrow keys are on.
const refAt = ref<{ start: number; query: string } | null>(null)
const refActive = ref(0)
const refOptions = computed(() => (refAt.value && props.references ? refItems(props.references, refAt.value.query) : []))
const refOpen = computed(() => refOptions.value.length > 0)
const refId = useId()
const textarea = () => box.value?.querySelector('textarea')

// refSync opens, narrows or closes the menu from the text before the caret.
function refSync() {
  const el = textarea()
  const at = props.references && el ? refQuery(el.value, el.selectionStart) : null
  if (at?.query !== refAt.value?.query) refActive.value = 0
  refAt.value = at
}

// The number of options is said as it changes, not the options.
watch(
  () => (refAt.value ? refOptions.value.length : -1),
  (n) => {
    if (n >= 0) announce(n === 0 ? 'No references match' : `${n} ${plural(n, 'reference')}`)
  },
)
watch(refActive, (i) => void nextTick(() => document.getElementById(`${refId}-${i}`)?.scrollIntoView({ block: 'nearest' })))

function pickRef(item: RefItem | undefined) {
  const el = textarea()
  const at = refAt.value
  if (!item || !el || !at) return
  const next = refPick(body.value, at.start, el.selectionStart, item.ref)
  body.value = next.body
  refAt.value = null
  void nextTick(() => {
    el.focus()
    el.setSelectionRange(next.caret, next.caret)
  })
}

// While the % menu is open the arrows move, Enter picks instead of a new
// line (Ctrl/Cmd+Enter still sends) and Escape closes it.
function onKeydown(ev: KeyboardEvent) {
  if (!refAt.value || ev.isComposing) return
  const n = refOptions.value.length
  if (ev.key === 'Escape') {
    ev.preventDefault()
    ev.stopPropagation()
    refAt.value = null
  } else if (n && (ev.key === 'ArrowDown' || ev.key === 'ArrowUp')) {
    ev.preventDefault()
    refActive.value = (refActive.value + (ev.key === 'ArrowDown' ? 1 : n - 1)) % n
  } else if (n && ev.key === 'Enter' && !ev.ctrlKey && !ev.metaKey && !ev.shiftKey) {
    ev.preventDefault()
    pickRef(refOptions.value[refActive.value])
  }
}

// A caret moved by the side arrows, Home or End can leave the "%query".
function onKeyup(ev: KeyboardEvent) {
  if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(ev.key)) refSync()
}

function onInput() {
  refSync()
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
    refAt.value = null
  }
}
onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

const menu =
  'absolute top-[calc(100%+6px)] left-2.5 z-20 w-[min(340px,calc(100%-20px))] rounded-[10px] border border-(--border-strong) bg-(--surface) p-1.5 shadow-lg'
const option = 'flex w-full items-baseline gap-2.5 rounded-[7px] px-2 py-1.5 text-left font-normal text-(--text)'
// The chat's composer sits at the bottom of its card: its % menu opens up.
const refMenu = menu.replace('top-[calc(100%+6px)]', 'bottom-[calc(100%+6px)]')
// The text is the % menu's combobox, only where there is one.
const refAttrs = computed(() =>
  props.references
    ? {
        role: 'combobox',
        'aria-autocomplete': 'list',
        'aria-expanded': String(refOpen.value),
        'aria-controls': refOpen.value ? refId : undefined,
        'aria-activedescendant': refOpen.value ? `${refId}-${refActive.value}` : undefined,
      }
    : {},
)
</script>

<template>
  <div class="composer" :class="mode">
    <slot name="controls" />
    <div v-if="blocked" class="rounded-(--radius) border border-(--border) bg-(--surface) px-3.5 py-3">
      <p class="mt-0 mb-2.5">{{ blocked }}</p>
      <UButton v-if="connectUrl" :href="connectUrl" color="primary" label="Connect GitLab" />
    </div>
    <!-- The field ties the error line to the text (aria-describedby and
         aria-invalid); its root keeps the composer's own type size. Its
         error prop is Boolean or String, so Vue casts "" (no error) to true:
         pass undefined instead. -->
    <UFormField v-else :error="error || undefined" :ui="{ root: 'text-[length:inherit]', error: 'mt-1.5 text-[13px] text-(--tone-danger-fg)' }">
      <div
        ref="box"
        class="relative rounded-xl border border-(--border-strong) bg-(--surface) shadow-(--shadow) focus-within:border-(--focus) focus-within:ring-3 focus-within:ring-(--focus)/20"
        :class="dragging && 'border-dashed bg-(--surface-2)'"
        :data-dragging="dragging || undefined"
        @dragover="onDragover"
        @dragleave="onDragleave"
        @drop="onDrop"
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
          v-bind="refAttrs"
          @input="onInput"
          @click="refSync"
          @keydown="onKeydown"
          @keyup="onKeyup"
          @keydown.meta.enter="send()"
          @keydown.ctrl.enter="send()"
          @paste="onPaste"
        />
        <div class="flex items-center gap-2 pt-1.5 pr-2 pb-2" :class="[mode === 'note' ? 'pl-2.5' : 'pl-3.5', attachments && 'flex-wrap']">
          <AttachmentPicker v-if="attachments" ref="picker" v-model="attachments" :disabled="disabled" />
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
            :disabled="busy || disabled || !canSend"
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
        <!-- The options keep the focus in the text (mousedown.prevent); the
             keys move through them (aria-activedescendant). -->
        <div v-if="refAt" :class="refMenu" @click.stop>
          <div :id="`${refId}-label`" class="px-2 pt-1.5 pb-0.5 text-[11px] tracking-[0.05em] text-(--text-3) uppercase">
            Milestones and drafts
          </div>
          <div v-if="refOpen" :id="refId" role="listbox" :aria-labelledby="`${refId}-label`" class="max-h-[264px] overflow-y-auto">
            <div
              v-for="(r, i) in refOptions"
              :id="`${refId}-${i}`"
              :key="r.ref"
              role="option"
              :aria-selected="i === refActive"
              :class="[option, 'cursor-pointer', i === refActive && 'bg-(--surface-2)']"
              @mousedown.prevent
              @mousemove="refActive = i"
              @click="pickRef(r)"
            >
              <b class="font-mono text-[12.5px] font-semibold whitespace-nowrap">{{ r.ref }}</b>
              <span class="min-w-0 flex-1 truncate text-xs">{{ r.label }}</span>
              <span class="text-[11px] whitespace-nowrap text-(--text-3)">{{ r.state }}</span>
            </div>
          </div>
          <p v-else class="m-0 px-2 py-1.5 text-xs text-(--text-3)">No milestone or draft matches.</p>
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
   A note composer (the Cockpit's, on top) scrolls with the page (R3). */
.composer.note {
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
