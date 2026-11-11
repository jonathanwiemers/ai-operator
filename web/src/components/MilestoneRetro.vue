<script setup lang="ts">
import { computed, ref, watch } from 'vue'
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
import type { FollowUpMilestoneView, MilestoneView } from '../types'

// A milestone's Retrospective tab (R12): when it was posted and its
// comment, the text, the cost drivers and the leftovers with what came of
// them. Owners (permissions.followUp) tick open leftovers, ask for a draft
// of a follow-up milestone (the pilot's with permissions.draft, else the
// follow-ups' titles and bodies), edit it and create it (R14, R15). Text,
// titles, bodies and drafts are untrusted markdown: markdown.ts renders them
// to VNodes and the editor is a plain textarea, so HTML shows as text.
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
const dismissStep = ref<'idle' | 'confirm' | 'sent'>('idle')
watch(mayDismiss, (ok) => {
  if (!ok) dismissStep.value = 'idle'
})

async function dismiss() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  const path = `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}/milestones/${props.milestone.iid}/followups/dismiss`
  try {
    await post(path, {})
    dismissStep.value = 'sent'
  } catch (e) {
    dismissStep.value = 'idle'
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}

const draftHint = computed(() =>
  pilotTask.value
    ? 'The pilot writes a title and a description from the ticked leftovers.'
    : 'Starts from the ticked leftovers’ titles and bodies.',
)
</script>

<template>
  <section class="mretro">
    <div v-if="!r" class="empty">The retrospective is written when the autopilot is done.</div>
    <template v-else>
      <div v-if="r.postedAt || r.noteURL" class="meta">
        <span v-if="r.postedAt">Posted <RelTime :time="r.postedAt" /></span>
        <ExtLink v-if="r.noteURL" :href="r.noteURL">Comment on GitLab</ExtLink>
      </div>
      <div v-if="r.failed" class="banner warning">No retrospective: {{ r.failed }}</div>

      <div v-if="r.text" class="block text"><Markdown variant="doc" :text="r.text" /></div>
      <p v-else-if="!r.failed" class="note">The text of this retrospective is not stored.</p>

      <div v-if="drivers.length" class="block">
        <h3>Cost drivers</h3>
        <ul class="drivers">
          <li v-for="(d, k) in drivers" :key="d.kind + d.label">
            <span class="label">{{ d.label }}</span>
            <Badge tone="muted">{{ d.kind }}</Badge>
            <span class="num">{{ d.costKnown ? cost(d.costUSD) : 'n/a' }}</span>
            <span class="num">{{ tokenCount(d.tokens) }} tok</span>
            <span class="num pct">{{ driverPct[k] }}</span>
          </li>
        </ul>
      </div>

      <div v-if="followUps.length" class="block">
        <h3>Leftovers <span>{{ followUps.length }}</span></h3>
        <ul class="followups">
          <li v-for="f in followUps" :key="f.number">
            <div class="head">
              <input
                v-if="tickable(f.number)"
                v-model="ticked"
                type="checkbox"
                class="tick"
                :value="f.number"
                :aria-label="`Take leftover ${f.number} into a follow-up milestone`"
              />
              <span class="n">{{ f.number }}.</span>
              <span class="title">{{ f.title }}</span>
              <ExtLink v-if="f.targetURL" :href="f.targetURL" class="state">
                <Badge :tone="followUpStateTone(f.state)">{{ followUpStateLabel(f) }}</Badge>
              </ExtLink>
              <Badge v-else class="state" :tone="followUpStateTone(f.state)">{{ followUpStateLabel(f) }}</Badge>
            </div>
            <details v-if="f.body">
              <summary>Details</summary>
              <Markdown variant="doc" :text="f.body" />
            </details>
          </li>
        </ul>

        <div v-if="created" class="banner info created">
          Created the milestone %{{ created.iid }}:
          <RouterLink :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(created.iid) } }">
            open it in the Desk
          </RouterLink>
          ·
          <ExtLink :href="created.webURL">on GitLab</ExtLink>. Its autopilot has started; the leftovers show as taken
          after the operator's next poll.
        </div>

        <div v-if="owner && step === 'pick' && openNumbers.length" class="actions">
          <button type="button" class="btn" :disabled="!ticked.length || busy" @click="askDraft">
            Draft follow-up milestone
          </button>
          <span class="hint">{{ ticked.length ? draftHint : 'Tick the leftovers to take over.' }}</span>
        </div>
        <div v-if="pilotDraft" class="actions">
          <button type="button" class="btn" @click="edit(pilotDraft.numbers, pilotDraft.title ?? '', pilotDraft.description ?? '')">
            Edit the pilot's draft
          </button>
          <span class="hint">
            From leftovers {{ pilotDraft.numbers.join(', ') }}<template v-if="pilotDraft.at">, written <RelTime :time="pilotDraft.at" /></template>:
            {{ pilotDraft.title }}
          </span>
        </div>
        <div v-if="step === 'drafting' || othersDrafting" class="actions">
          <span class="drafting">The pilot is drafting…</span>
          <button v-if="step === 'drafting'" type="button" class="btn" @click="discard">Stop waiting</button>
        </div>
        <div v-if="mayDismiss && step === 'pick'" class="actions">
          <template v-if="dismissStep === 'idle'">
            <button type="button" class="btn" :disabled="busy" @click="dismissStep = 'confirm'">Dismiss remaining</button>
            <span class="hint">Lets the milestone close without waiting for them.</span>
          </template>
          <template v-else-if="dismissStep === 'confirm'">
            <span class="hint confirm">
              Dismiss the open leftovers? This replies <code>create none</code> in the retrospective's thread in your GitLab name;
              each can still be opened later.
            </span>
            <button type="button" class="btn danger" :disabled="busy" @click="dismiss">Dismiss them</button>
            <button type="button" class="btn" :disabled="busy" @click="dismissStep = 'idle'">Cancel</button>
          </template>
          <span v-else class="drafting">Dismissing… the operator settles them on its next poll.</span>
        </div>
        <div v-if="error" class="banner danger">{{ error }}</div>

        <div v-if="step === 'editing'" class="editor">
          <h3>Follow-up milestone <span>from leftovers {{ edited.join(', ') }}</span></h3>
          <div class="panes">
            <div class="pane">
              <label for="fu-title">Title</label>
              <input id="fu-title" v-model="title" type="text" :maxlength="maxTitle" autocomplete="off" />
              <label for="fu-desc">Description</label>
              <textarea id="fu-desc" v-model="description" rows="14" spellcheck="true" />
              <p class="hint">Plain markdown; HTML shows as text. The operator adds the spec, autopilot, owners and follow-up lines below it.</p>
            </div>
            <div class="pane">
              <span class="label-like">Preview</span>
              <div class="preview">
                <h4 class="ptitle">{{ title.trim() || 'Untitled' }}</h4>
                <Markdown variant="doc" :text="description" />
              </div>
            </div>
          </div>
          <p class="autostart">Autopilot starts at once: the new milestone is planned and its issues are worked on without further confirmation.</p>
          <div class="actions">
            <button type="button" class="btn primary" :disabled="!canCreate" @click="create">
              Create milestone and start autopilot
            </button>
            <button type="button" class="btn" :disabled="busy" @click="discard">Discard</button>
          </div>
        </div>
      </div>
    </template>
  </section>
</template>
<style scoped>
.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  font-size: 13px;
  color: var(--text-2);
}
.banner {
  margin-top: 12px;
}
.block {
  margin-top: 16px;
}
.text {
  padding: 14px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 14px;
}
.note {
  margin: 12px 0 0;
  font-size: 13px;
  color: var(--text-3);
}
ul {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
}
li {
  padding: 8px 14px;
  border-top: 1px solid var(--border);
  font-size: 13px;
}
li:first-child {
  border-top: 0;
}
.drivers li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 64px 72px 44px;
  align-items: center;
  gap: 10px;
}
.label,
.title {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.num {
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
  text-align: right;
  white-space: nowrap;
}
.pct {
  color: var(--text);
}
.followups .head {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.n {
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
}
.title {
  flex: 1;
  min-width: 0;
}
.state {
  flex: none;
}
details {
  margin: 6px 0 2px 22px;
}
summary {
  cursor: pointer;
  font-size: 12.5px;
  color: var(--text-3);
}
details[open] summary {
  margin-bottom: 6px;
}
.tick {
  flex: none;
  margin: 0;
  align-self: center;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  margin-top: 12px;
}
.hint,
.drafting,
.autostart {
  font-size: 13px;
  color: var(--text-2);
}
.hint {
  overflow-wrap: anywhere;
}
.drafting {
  margin: 12px 0 0;
  font-style: italic;
}
.confirm {
  flex-basis: 100%;
}
.btn.danger {
  color: var(--tone-danger-fg);
}
.created {
  display: block;
}
.editor {
  margin-top: 16px;
}
.editor h3 span {
  font-weight: 400;
}
.panes {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
}
.pane {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.pane label,
.label-like {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text-2);
}
.pane input,
.pane textarea {
  box-sizing: border-box;
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  font-size: 14px;
}
.pane textarea {
  resize: vertical;
  min-height: 220px;
  font-family: var(--mono);
  font-size: 13px;
}
.pane .hint {
  margin: 0;
}
.preview {
  flex: 1;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 14px;
  overflow-wrap: anywhere;
}
.ptitle {
  margin: 0 0 10px;
  font-size: 16px;
}
.autostart {
  margin: 12px 0 0;
  font-weight: 600;
  color: var(--text);
}
.empty {
  padding: 32px;
  text-align: center;
  color: var(--text-3);
}
@media (max-width: 759.98px) {
  .drivers li {
    grid-template-columns: auto 1fr auto auto;
  }
  .drivers .label {
    grid-column: 1 / -1;
  }
  .drivers .num {
    text-align: left;
  }
  .followups .head {
    flex-wrap: wrap;
  }
  .panes {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
