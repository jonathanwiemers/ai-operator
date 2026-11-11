<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { ApiError, chatPostError, getTaskChat, post, postTaskChat } from '../api'
import type { ChatCommand } from '../chatState'
import {
  draftRefused,
  draftWritten,
  maxTitle,
  selectable,
  takenMessage,
  templateDraft,
} from '../followUpDraft'
import { live } from '../live'
import { commandAnswer } from '../pauseCard'
import Badge from './Badge.vue'
import ExtLink from './ExtLink.vue'
import RelTime from './RelTime.vue'
import Markdown from './Markdown.vue'
import { cost, tokenCount } from '../format'
import { driverShares, followUpStateLabel, followUpStateTone } from '../retrospective'
import type { CostDriverView, FollowUpMilestoneView, MilestoneView } from '../types'
import DataList, { type DataListColumn } from '../ui/DataList.vue'
import { useBackClose } from '../ui/useBackClose'

const UModal = defineAsyncComponent(() => import('@nuxt/ui/components/Modal.vue'))

// A milestone's Retrospective tab (R12): when it was posted and its
// comment, the text, the cost drivers and the leftovers with what came of
// them. Owners (permissions.followUp) tick open leftovers, ask for a draft
// of a follow-up milestone (the pilot's with permissions.draft, else the
// follow-ups' titles and bodies), edit it and create it (R14, R15). Text,
// titles, bodies and drafts are untrusted markdown: markdown.ts renders them
// to VNodes and the editor is a plain textarea, so HTML shows as text.
//
// Utilities only (docs/ui.md, "Milestone Retrospective"): the cost drivers
// are a DataList, stacked on phones; the actions are UButtons, 44 px high
// on phones, where action rows and the editor's panes stack; the inputs are
// 16 px there. Dismissing asks in a UModal, closed by back too.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView }>()

const r = computed(() => props.milestone.retrospective)
const drivers = computed(() => r.value?.costDrivers ?? [])
const driverPct = computed(() => driverShares(drivers.value))
const followUps = computed(() => r.value?.followUps ?? [])
const perms = computed(() => props.milestone.permissions)
const owner = computed(() => !!perms.value?.followUp)
const pilotTask = computed(() => (perms.value?.draft ? props.milestone.pilot?.task : undefined))
const draft = computed(() => r.value?.draft)
// claimed: the leftovers of the milestone created here, until the operator's
// next poll marks them taken.
const claimed = ref<number[]>([])
const openNumbers = computed(() =>
  selectable(followUps.value)
    .map((f) => f.number)
    .filter((n) => !claimed.value.includes(n)),
)

// The follow-ups ticked; the editor holds the draft for edited.
const ticked = ref<number[]>([])
const step = ref<'pick' | 'drafting' | 'editing'>('pick')
const edited = ref<number[]>([])
const title = ref('')
const description = ref('')
const error = ref('')
const busy = ref(false)
const created = ref<FollowUpMilestoneView | null>(null)
// The draft command sent: its chat id, task and the draft seen before.
const sent = ref<{ id: number; task: string; before?: string } | null>(null)

const tickable = (n: number) => owner.value && step.value === 'pick' && openNumbers.value.includes(n)
// Ticked numbers that are no longer open drop out on the live reload.
watch(openNumbers, (open) => (ticked.value = ticked.value.filter((n) => open.includes(n))))

const tickedFollowUps = computed(() => followUps.value.filter((f) => ticked.value.includes(f.number)))
const pilotDraft = computed(() => {
  const d = draft.value
  if (!owner.value || step.value !== 'pick' || !d || d.pending || d.error || !d.title) return undefined
  return d.numbers.every((n) => openNumbers.value.includes(n)) ? d : undefined
})
const othersDrafting = computed(() => step.value === 'pick' && !!draft.value?.pending)

function edit(numbers: number[], t: string, d: string) {
  edited.value = [...numbers]
  title.value = t
  description.value = d
  error.value = ''
  step.value = 'editing'
}

async function askDraft() {
  const nums = [...ticked.value].sort((a, b) => a - b)
  if (!nums.length || busy.value) return
  error.value = ''
  created.value = null
  const task = pilotTask.value
  if (!task) {
    const t = templateDraft(props.milestone.title, tickedFollowUps.value)
    edit(nums, t.title, t.description)
    return
  }
  busy.value = true
  try {
    const cmd: ChatCommand = { cmd: 'draft', followUps: nums }
    const res = await postTaskChat(props.namespace, props.name, task, {
      body: JSON.stringify(cmd),
      clientKey: crypto.randomUUID(),
      kind: 'command',
    })
    sent.value = { id: res.id, task, before: draft.value?.at }
    edited.value = nums
    step.value = 'drafting'
    void loadAnswer()
  } catch (e) {
    error.value = chatPostError(e)
  } finally {
    busy.value = false
  }
}

// The pilot's draft lands through the live reload of the milestone.
watch(draft, (d) => {
  const s = sent.value
  if (step.value !== 'drafting' || !s || !draftWritten(d, s.before) || !d) return
  sent.value = null
  if (d.error) {
    error.value = `The pilot wrote no draft: ${d.error}`
    step.value = 'pick'
    return
  }
  edit(d.numbers, d.title ?? '', d.description ?? '')
})

// loadAnswer reads the operator's answer to the draft command: a refusal
// ends the wait, an acceptance keeps it until the draft lands.
async function loadAnswer() {
  const s = sent.value
  if (!s || step.value !== 'drafting') return
  try {
    const page = await getTaskChat(props.namespace, props.name, s.task, { limit: 50 })
    if (sent.value !== s) return
    const a = commandAnswer(page.messages, s.id)
    if (a.state === 'failed' || (a.state === 'answered' && draftRefused(a.text))) {
      sent.value = null
      step.value = 'pick'
      error.value = a.state === 'failed' ? a.reason : a.text || 'The pilot refused the draft.'
    }
  } catch {
    /* best-effort; the next live update tries again */
  }
}
watch(() => live.version, () => void loadAnswer())

function discard() {
  sent.value = null
  step.value = 'pick'
  error.value = ''
}

const titleOK = computed(() => {
  const t = title.value.trim()
  return t.length > 0 && [...t].length <= maxTitle && !/[\r\n]/.test(t)
})
const canCreate = computed(() => titleOK.value && description.value.trim().length > 0 && !busy.value)

async function create() {
  if (!canCreate.value) return
  busy.value = true
  error.value = ''
  const path = `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/milestones/${props.milestone.iid}/followup`
  try {
    created.value = await post<FollowUpMilestoneView>(path, {
      followUps: edited.value,
      title: title.value.trim(),
      description: description.value.trim(),
    })
    claimed.value = [...claimed.value, ...edited.value]
    ticked.value = []
    step.value = 'pick'
  } catch (e) {
    const taken = e instanceof ApiError && e.status === 409 ? e.data.taken : undefined
    if (Array.isArray(taken) && taken.length) {
      error.value = takenMessage(taken as number[], followUps.value)
    } else {
      error.value = e instanceof Error ? e.message : String(e)
    }
  } finally {
    busy.value = false
  }
}

// Dismiss (R17): owners (permissions.dismiss) reply "create none" in the
// retrospective's thread through the Desk; the operator settles the
// leftovers on its next poll, which the live reload shows.
const mayDismiss = computed(() => !!perms.value?.dismiss)
const dismissSent = ref(false)
// confirming: the confirmation dialog is open.
const confirming = ref(false)
useBackClose(confirming)
watch(mayDismiss, (ok) => {
  if (ok) return
  dismissSent.value = false
  confirming.value = false
})

async function dismiss() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  const path = `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/milestones/${props.milestone.iid}/followups/dismiss`
  try {
    await post(path, {})
    dismissSent.value = true
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
    confirming.value = false
  }
}

const driverColumns: DataListColumn[] = [
  { key: 'label', label: 'Driver', class: 'font-semibold [overflow-wrap:anywhere]' },
  { key: 'kind', label: 'Kind' },
  { key: 'cost', label: 'Cost', numeric: true, class: 'whitespace-nowrap text-(--text-2)' },
  { key: 'tokens', label: 'Tokens', numeric: true, class: 'whitespace-nowrap text-(--text-2)' },
  { key: 'share', label: 'Share', numeric: true, class: 'whitespace-nowrap' },
]
// driverRows: the drivers with their share, keyed for the DataList.
const driverRows = computed(() =>
  drivers.value.map((d, k): CostDriverView & { key: string; share: string } => ({
    ...d,
    key: `${d.kind}:${d.label}`,
    share: driverPct.value[k] ?? '',
  })),
)

const draftHint = computed(() =>
  pilotTask.value
    ? 'The pilot writes a title and a description from the ticked leftovers.'
    : 'Starts from the ticked leftovers’ titles and bodies.',
)

// act: a button, 44 px high on phones.
const act = { color: 'neutral', variant: 'outline', size: 'sm', class: 'min-h-11 justify-center tablet:min-h-0' } as const
// stack: a row of controls and their hint, a column on phones.
const stack =
  'mt-3 flex min-w-0 flex-col items-stretch gap-1.5 tablet:flex-row tablet:flex-wrap tablet:items-center tablet:gap-x-3 tablet:gap-y-2'
const hint = 'text-[13px] text-(--text-2) [overflow-wrap:anywhere]'
const waitNote = 'text-[13px] text-(--text-2) italic'
// tap: a link, at least 44 px high on phones.
const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
const box = 'rounded-(--radius-sm) border border-(--border) bg-(--surface)'
const fieldLabel = 'text-[12.5px] font-semibold text-(--text-2)'
// The editor's fields: 16 px on phones, so iOS does not zoom on focus.
const titleUI = { root: 'w-full', base: 'text-base/5 tablet:text-sm/5' }
const descriptionUI = { root: 'w-full', base: 'min-h-[220px] resize-y font-mono text-base/5 tablet:text-[13px]/5' }
</script>

<template>
  <section>
    <div v-if="!r" class="empty">The retrospective is written when the autopilot is done.</div>
    <template v-else>
      <div v-if="r.postedAt || r.noteURL" class="flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-(--text-2)" data-retro-facts>
        <span v-if="r.postedAt">Posted <RelTime :time="r.postedAt" /></span>
        <ExtLink v-if="r.noteURL" :href="r.noteURL" :class="tap">Comment on GitLab</ExtLink>
      </div>
      <div v-if="r.failed" class="banner warning [overflow-wrap:anywhere]">No retrospective: {{ r.failed }}</div>

      <div v-if="r.text" class="mt-4 px-4 py-3.5 text-sm" :class="box"><Markdown variant="doc" :text="r.text" /></div>
      <p v-else-if="!r.failed" class="mt-3 mb-0 text-[13px] text-(--text-3)">The text of this retrospective is not stored.</p>

      <div v-if="drivers.length" class="mt-4" data-drivers>
        <h3>Cost drivers</h3>
        <div class="overflow-hidden text-[13px]" :class="box">
          <DataList :columns="driverColumns" :rows="driverRows" :row-key="(d) => d.key">
            <template #cell-kind="{ row }"><Badge tone="muted">{{ row.kind }}</Badge></template>
            <template #cell-cost="{ row }">{{ row.costKnown ? cost(row.costUSD) : 'n/a' }}</template>
            <template #cell-tokens="{ row }">{{ tokenCount(row.tokens) }} tok</template>
          </DataList>
        </div>
      </div>

      <div v-if="followUps.length" class="mt-4">
        <h3>Leftovers <span>{{ followUps.length }}</span></h3>
        <ul class="m-0 list-none p-0" :class="box">
          <li
            v-for="f in followUps"
            :key="f.number"
            class="border-t border-(--border) px-3.5 py-2 text-[13px] first:border-t-0"
            data-followup
          >
            <div class="flex flex-wrap items-center gap-x-2 gap-y-1 tablet:flex-nowrap tablet:items-baseline">
              <label
                v-if="tickable(f.number)"
                class="-ms-2.5 flex min-h-11 min-w-11 shrink-0 cursor-pointer items-center justify-center tablet:m-0 tablet:min-h-0 tablet:min-w-0 tablet:self-center"
              >
                <input
                  v-model="ticked"
                  type="checkbox"
                  class="m-0 size-4"
                  :value="f.number"
                  :aria-label="`Take leftover ${f.number} into a follow-up milestone`"
                />
              </label>
              <span class="whitespace-nowrap tabular-nums text-(--text-3)">{{ f.number }}.</span>
              <span class="min-w-[min(100%,10rem)] flex-1 font-semibold [overflow-wrap:anywhere]">{{ f.title }}</span>
              <ExtLink v-if="f.targetURL" :href="f.targetURL" class="shrink-0" :class="tap">
                <Badge :tone="followUpStateTone(f.state)">{{ followUpStateLabel(f) }}</Badge>
              </ExtLink>
              <Badge v-else class="shrink-0" :tone="followUpStateTone(f.state)">{{ followUpStateLabel(f) }}</Badge>
            </div>
            <details v-if="f.body" class="group ms-[22px] mt-1.5 mb-0.5">
              <summary
                class="min-h-11 cursor-pointer py-3 text-[12.5px] text-(--text-3) group-open:mb-1.5 tablet:min-h-0 tablet:py-0"
              >
                Details
              </summary>
              <Markdown variant="doc" :text="f.body" />
            </details>
          </li>
        </ul>

        <div v-if="created" class="banner info" role="status">
          <span class="min-w-0 [overflow-wrap:anywhere]">
            Created the milestone %{{ created.iid }}:
            <RouterLink :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(created.iid) } }">
              open it in the Desk
            </RouterLink>
            ·
            <ExtLink :href="created.webURL">on GitLab</ExtLink>. Its autopilot has started; the leftovers show as taken
            after the operator's next poll.
          </span>
        </div>

        <div v-if="owner && step === 'pick' && openNumbers.length" :class="stack" data-draft>
          <UButton v-bind="act" :disabled="!ticked.length || busy" label="Draft follow-up milestone" @click="askDraft" />
          <span :class="hint">{{ ticked.length ? draftHint : 'Tick the leftovers to take over.' }}</span>
        </div>
        <div v-if="pilotDraft" :class="stack">
          <UButton
            v-bind="act"
            label="Edit the pilot's draft"
            @click="edit(pilotDraft.numbers, pilotDraft.title ?? '', pilotDraft.description ?? '')"
          />
          <span :class="hint">
            From leftovers {{ pilotDraft.numbers.join(', ') }}<template v-if="pilotDraft.at">, written <RelTime :time="pilotDraft.at" /></template>:
            {{ pilotDraft.title }}
          </span>
        </div>
        <div v-if="step === 'drafting' || othersDrafting" :class="stack">
          <span :class="waitNote">The pilot is drafting…</span>
          <UButton v-if="step === 'drafting'" v-bind="act" label="Stop waiting" @click="discard" />
        </div>
        <div v-if="mayDismiss && step === 'pick'" :class="stack" data-dismiss>
          <span v-if="dismissSent" :class="waitNote" role="status">Dismissing… the operator settles them on its next poll.</span>
          <template v-else>
            <UButton v-bind="act" :disabled="busy" label="Dismiss remaining" @click="confirming = true" />
            <span :class="hint">Lets the milestone close without waiting for them.</span>
          </template>
        </div>
        <div v-if="error" class="banner danger [overflow-wrap:anywhere]" role="alert">{{ error }}</div>

        <div v-if="step === 'editing'" class="mt-4" data-editor>
          <h3>Follow-up milestone <span class="font-normal">from leftovers {{ edited.join(', ') }}</span></h3>
          <div class="grid grid-cols-1 gap-3 tablet:grid-cols-2">
            <div class="flex min-w-0 flex-col gap-1.5">
              <label for="fu-title" :class="fieldLabel">Title</label>
              <UInput id="fu-title" v-model="title" :maxlength="maxTitle" autocomplete="off" :ui="titleUI" />
              <label for="fu-desc" :class="fieldLabel">Description</label>
              <UTextarea id="fu-desc" v-model="description" :rows="14" spellcheck="true" :ui="descriptionUI" />
              <p class="m-0" :class="hint">
                Plain markdown; HTML shows as text. The operator adds the spec, autopilot, owners and follow-up lines below it.
              </p>
            </div>
            <div class="flex min-w-0 flex-col gap-1.5">
              <span :class="fieldLabel">Preview</span>
              <div class="flex-1 px-3.5 py-3 text-sm [overflow-wrap:anywhere]" :class="box">
                <h4 class="m-0 mb-2.5 text-base">{{ title.trim() || 'Untitled' }}</h4>
                <Markdown variant="doc" :text="description" />
              </div>
            </div>
          </div>
          <p class="mt-3 mb-0 text-[13px] font-semibold text-(--text)">
            Autopilot starts at once: the new milestone is planned and its issues are worked on without further confirmation.
          </p>
          <!-- mousedown.prevent: a press keeps the focus in the field, so the
               phone's bottom bar, hidden while a field has it, does not come
               back and move the button between press and release. -->
          <div :class="stack">
            <UButton
              v-bind="act"
              color="primary"
              variant="solid"
              :disabled="!canCreate"
              label="Create milestone and start autopilot"
              @mousedown.prevent
              @click="create"
            />
            <UButton v-bind="act" :disabled="busy" label="Discard" @mousedown.prevent @click="discard" />
          </div>
        </div>
      </div>
    </template>

    <!-- The dismiss confirmation (docs/ui.md, "Sheets"): closes by its
         scrim, button, Escape and back, but not while the reply is sent. -->
    <UModal
      v-if="mayDismiss"
      v-model:open="confirming"
      title="Dismiss the open leftovers?"
      :dismissible="!busy"
      close-icon="i-lucide-x"
      :close="{ disabled: busy, class: 'min-h-11 min-w-11 justify-center tablet:min-h-0 tablet:min-w-0' }"
      :ui="{
        overlay: 'z-40',
        content: 'z-40',
        title: 'text-[15px] font-bold',
        footer: 'flex-col-reverse items-stretch gap-2 tablet:flex-row tablet:justify-end',
      }"
    >
      <template #body>
        <p class="m-0 text-sm [overflow-wrap:anywhere]" data-dismiss-confirm>
          This replies <code>create none</code> in the retrospective's thread in your GitLab name; each can still be
          opened later.
        </p>
      </template>
      <template #footer>
        <UButton v-bind="act" :disabled="busy" label="Cancel" @click="confirming = false" />
        <UButton
          v-bind="act"
          color="error"
          variant="solid"
          :disabled="busy"
          :label="busy ? '…' : 'Dismiss them'"
          @click="dismiss"
        />
      </template>
    </UModal>
  </section>
</template>
