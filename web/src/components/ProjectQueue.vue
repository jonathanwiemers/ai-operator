<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UDropdownMenu, { type DropdownMenuItem } from '@nuxt/ui/components/DropdownMenu.vue'
import UIcon from '@nuxt/ui/components/Icon.vue'
import { computed, defineAsyncComponent, effectScope, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { chatPostError } from '../api'
import type { ChatCommand } from '../chatState'
import { getTaskChat, postTaskChat } from '../client'
import type { QueueEntry } from '../generated/views'
import { live } from '../live'
import { commandAnswer, type CommandAnswer } from '../pauseCard'
import { confirmText, inOrder, menuMoves, moveCommand, moved, queueActions, queueStateLabel, queueTone, reports } from '../projectQueue'
import { useDraggable } from 'vue-draggable-plus'
import { useBackClose } from '../ui/useBackClose'
import Badge from './Badge.vue'
import CostChip from './CostChip.vue'
import LocalText from './LocalText.vue'
import Markdown from './Markdown.vue'
import Truncated from '../ui/Truncated.vue'
const UModal = defineAsyncComponent(() => import('@nuxt/ui/components/Modal.vue'))

// ProjectQueue: a project's open autopilot milestones in run order with
// Pause / Resume, a drag handle and a row menu of moves (docs/contributing/web-ui.md,
// "Queue and Finishing"). Titles are text. Stream reloads the project on live
// updates; the answer loads here.
const props = defineProps<{ namespace: string; name: string; queue: QueueEntry[]; commands: boolean }>()

const sending = ref('')
const error = ref('')
// move: the iid a sent move command moves (0 for other commands).
const sent = ref<{ id: number; task: string; move: number } | null>(null)
const answer = ref<CommandAnswer>({ state: 'pending' })

// pending: the order a move asked for, shown until project.queue reports it
// (D662-P2).
const pending = ref<{ iid: number; order: number[] } | null>(null)
const rows = computed(() => (pending.value ? inOrder(props.queue, pending.value.order) : props.queue))
watch(
  () => props.queue,
  (q) => {
    const p = pending.value
    if (p && (reports(q, p.order) || !q.some((e) => e.iid === p.iid))) pending.value = null
  },
)
const awaitingMove = computed(() => !!pending.value && sent.value?.move === pending.value.iid && answer.value.state === 'pending')
const moveLocked = computed(() => !!sending.value || !!pending.value)

async function send(e: QueueEntry, cmd: ChatCommand, key: string, move = 0) {
  if (sending.value) return
  sending.value = key
  error.value = ''
  try {
    const r = await postTaskChat(props.namespace, props.name, e.task, { body: JSON.stringify(cmd), clientKey: crypto.randomUUID(), kind: 'command' })
    sent.value = { id: r.id, task: e.task, move }
    answer.value = { state: 'pending' }
    void loadAnswer()
  } catch (err) {
    if (move) pending.value = null
    error.value = chatPostError(err)
  } finally {
    sending.value = ''
  }
}

async function loadAnswer() {
  const s = sent.value
  if (!s || answer.value.state !== 'pending') return
  try {
    const page = await getTaskChat(props.namespace, props.name, s.task, { limit: 50 })
    if (sent.value !== s) return
    const a = (answer.value = commandAnswer(page.messages, s.id))
    // A refused move goes back with the operator's answer; an applied one
    // starts with "Moved". A redacted answer cannot tell, so the row waits.
    if (!s.move || pending.value?.iid !== s.move) return
    if (a.state === 'failed') rollback(a.reason)
    else if (a.state === 'answered' && !a.redacted && !a.text.startsWith('Moved')) rollback(a.text)
  } catch {
    /* best-effort; the next live update tries again */
  }
}
watch(() => live.version, () => void loadAnswer())

function rollback(why: string) {
  pending.value = null
  error.value = why
}

// confirming: a move above the running milestone waits for the modal. It
// keeps the order it was asked on: a sync that changes the order while the
// modal is open sends nothing, so Move never sends another move than the
// text named.
const confirming = ref<{ from: number; to: number; iid: number; text: string; order: number[] } | null>(null)
const confirmOpen = ref(false)
useBackClose(confirmOpen)
watch(confirmOpen, (open) => {
  if (open || !confirming.value) return
  // Closed by Cancel, Escape or back: nothing is sent.
  const c = confirming.value
  confirming.value = null
  focusRow(c.iid)
})

const iids = (list: readonly QueueEntry[]) => list.map((e) => e.iid)

function request(from: number, to: number) {
  const e = rows.value[from]
  if (moveLocked.value || !e || !moveCommand(rows.value, from, to)) return
  const text = confirmText(rows.value, from, to)
  if (text) {
    confirming.value = { from, to, iid: e.iid, text, order: iids(rows.value) }
    confirmOpen.value = true
    return
  }
  void move(from, to)
}

function confirmMove() {
  const c = confirming.value
  confirming.value = null
  confirmOpen.value = false
  if (!c) return
  const now = iids(rows.value)
  if (now.length !== c.order.length || now.some((iid, k) => iid !== c.order[k])) {
    error.value = 'The queue changed while you confirmed; nothing was moved. Try again.'
    focusRow(c.iid)
    return
  }
  void move(c.from, c.to)
}

async function move(from: number, to: number) {
  const list = rows.value
  const cmd = moveCommand(list, from, to)
  const e = list[from]
  if (!cmd || !e) return
  pending.value = { iid: e.iid, order: moved(list, from, to).map((r) => r.iid) }
  focusRow(e.iid)
  await send(e, cmd, `${e.iid}move`, e.iid)
}

// The moved row keeps the keyboard focus on its menu button.
const section = ref<HTMLElement | null>(null)
function focusRow(iid: number) {
  void nextTick(() => section.value?.querySelector<HTMLElement>(`[data-queue-menu="${iid}"]`)?.focus())
}

function menuItems(i: number): DropdownMenuItem[] {
  return menuMoves(rows.value, i).map((m) => ({ label: m.label, disabled: moveLocked.value, onSelect: () => request(i, m.to) }))
}

// Dragging (Decision 11): vue-draggable-plus is in this component's chunk,
// which Stream loads lazily (D662-P4), and starts only when commands are on.
// Vue owns the rows, so a drop puts the dragged row back and the new order
// renders from `rows`.
const list = ref<HTMLElement | null>(null)
let drag: { stop: () => void } | null = null
function stopDrag() {
  drag?.stop()
  drag = null
}
watch(
  [() => props.commands, list],
  ([on, el]) => {
    stopDrag()
    if (!on || !el) return
    let next: Node | null = null
    const scope = effectScope()
    const d = scope.run(() =>
      useDraggable(el, {
        handle: '[data-queue-handle]',
        animation: matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 150,
        // The same pointer drag in every browser, its ghost outside the
        // rows Vue renders.
        forceFallback: true,
        fallbackOnBody: true,
        onStart: (ev) => (next = ev.item.nextSibling),
        onUpdate: (ev) => {
          ev.from.insertBefore(ev.item, next)
          if (ev.oldIndex !== undefined && ev.newIndex !== undefined) request(ev.oldIndex, ev.newIndex)
        },
      }),
    )
    drag = { stop: () => (d?.destroy(), scope.stop()) }
  },
  { immediate: true, flush: 'post' },
)
onBeforeUnmount(stopDrag)
</script>

<template>
  <section v-if="queue.length" ref="section" class="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-1 text-[13px]" aria-labelledby="project-queue">
    <h2 id="project-queue" class="m-0 min-w-0 text-sm font-semibold">Queue</h2>
    <ol ref="list" class="m-0 grid min-w-0 list-none grid-cols-[minmax(0,1fr)] gap-0.5 p-0">
      <li v-for="(e, i) in rows" :key="e.iid" class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 rounded-md px-1.5 py-1 hover:bg-(--surface-2)" :data-queue-entry="e.iid">
        <!-- Pointer only; keyboard and screen reader use the row menu. -->
        <span v-if="commands" class="flex shrink-0 cursor-grab touch-none text-(--text-3)" aria-hidden="true" data-queue-handle>
          <UIcon name="i-lucide-grip-vertical" class="size-4" />
        </span>
        <span class="w-5 shrink-0 text-right tabular-nums text-(--text-3)">{{ i + 1 }}</span>
        <RouterLink :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(e.iid) } }" class="row-link min-w-0 flex-[1_1_12rem] [overflow-wrap:anywhere]">
          <span class="font-mono text-xs">%{{ e.iid }}</span> {{ e.title }}
        </RouterLink>
        <Badge :tone="queueTone(e)">{{ queueStateLabel(e) }}</Badge>
        <Badge v-if="pending?.iid === e.iid" tone="progress" data-queue-pending>Moving…</Badge>
        <span class="text-xs text-(--text-3) tabular-nums">{{ e.done }}/{{ e.total }} issues</span>
        <CostChip :cost="e" small />
        <span v-if="commands" class="ml-auto flex flex-wrap gap-1">
          <UButton
            v-for="a in queueActions(e)"
            :key="a.id"
            color="neutral"
            variant="outline"
            size="xs"
            :disabled="!!sending || awaitingMove"
            :label="sending === `${e.iid}${a.id}` ? '…' : a.label"
            @click="send(e, a.cmd, `${e.iid}${a.id}`)"
          />
          <UDropdownMenu v-if="rows.length > 1" :items="menuItems(i)" :content="{ align: 'end' }" :ui="{ content: 'z-40', item: 'min-h-11 tablet:min-h-0' }">
            <UButton color="neutral" variant="outline" size="xs" icon="i-lucide-ellipsis" :aria-label="`Move %${e.iid}`" :data-queue-menu="e.iid" />
          </UDropdownMenu>
        </span>
        <!-- Why it paused: untrusted markdown, two lines that open on tap;
             refs and code tokens wrap anywhere. -->
        <Truncated
          v-if="e.state === 'paused' && e.pausedReason"
          class="min-w-0 basis-full ps-7 text-xs text-(--text-2) [overflow-wrap:anywhere] [&_code]:whitespace-normal"
          :lines="2"
          data-queue-reason
        >
          <Markdown mode="inline" :text="e.pausedReason" />
        </Truncated>
      </li>
    </ol>
    <div v-if="error" class="[overflow-wrap:anywhere] text-(--text-3)" role="alert">{{ error }}</div>
    <div v-else-if="sent" class="[overflow-wrap:anywhere] text-(--text-3)" role="status">
      <LocalText v-if="answer.state === 'failed'" :text="answer.reason" />
      <template v-else>{{ answer.state === 'pending' ? 'Sent…' : answer.redacted ? 'Done.' : answer.text }}</template>
    </div>
    <UModal v-model:open="confirmOpen" title="Move above the running milestone?" :ui="{ overlay: 'z-40', content: 'z-40 tablet:max-w-md' }">
      <template #body>
        <p class="m-0 text-sm" data-queue-confirm>{{ confirming?.text }}</p>
      </template>
      <template #footer>
        <UButton color="neutral" variant="outline" label="Cancel" @click="confirmOpen = false" />
        <UButton label="Move" data-queue-confirm-move @click="confirmMove" />
      </template>
    </UModal>
  </section>
</template>
