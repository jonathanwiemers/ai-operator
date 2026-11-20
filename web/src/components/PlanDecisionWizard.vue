<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
// The decision review wizard (R7): one step per decision with the
// preselected option, Other, a step list, and a last step with the summary,
// more text and Send. It works on the decisions it opened on, so a poll does
// not change a step under the owner; the picks stay in sessionStorage.
import { computed, defineAsyncComponent, onUnmounted, ref, watch } from 'vue'
import { chatPostError } from '../api'
import { DecisionsChanged, getPlan, postPlanChat } from '../client'
import * as w from '../planWizard'
import UButton from '@nuxt/ui/components/Button.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import Badge from './Badge.vue'
import Markdown from './Markdown.vue'
import type { PlanDecision } from '../generated/api'
import type { PlanView } from '../generated/views'
import { useBackClose } from '../ui/useBackClose'

const UModal = defineAsyncComponent(() => import('@nuxt/ui/components/Modal.vue'))
const props = defineProps<{ namespace: string; name: string; plan: string; view: PlanView }>()
const emit = defineEmits<{ sent: []; view: [PlanView] }>()
const open = defineModel<boolean>('open', { required: true })
const [decisions, picks, clientKey, text] = [ref<PlanDecision[]>([]), ref<w.Picks>({}), ref(''), ref('')]
const [step, marked, error, busy] = [ref(0), ref<string[]>([]), ref(''), ref(false)]
const key = () => w.wizardKey(props.namespace, props.name, props.plan)
const d = computed(() => decisions.value[step.value])
const pick = computed(() => (d.value ? w.pickOf(d.value, picks.value) : 0))
const pre = computed(() => (d.value ? w.preselect(d.value) : null))
const sum = computed(() => w.summary(decisions.value, picks.value))
const blocked = computed(() => w.wizardBlocked(props.view.phase))
const setPick = (p: w.WizardPick) => d.value && (picks.value = { ...picks.value, [d.value.id]: { version: d.value.version, pick: p } })

// Full screen on phones (the search palette's rule).
const phone = window.matchMedia('(max-width: 759.98px)')
const fullscreen = ref(phone.matches)
const onPhone = () => (fullscreen.value = phone.matches)
phone.addEventListener('change', onPhone)
onUnmounted(() => phone.removeEventListener('change', onPhone))

useBackClose(open)
function take(list: PlanDecision[], old: w.Picks) {
  [decisions.value, picks.value] = [list, w.mergePicks(old, list)]
}
watch(open, (o) => {
  if (!o) return
  const s = w.readPicks(key())
  take(props.view.decisions ?? [], s?.picks ?? {})
  ;[clientKey.value, text.value, marked.value, error.value] = [s?.clientKey || crypto.randomUUID(), s?.text ?? '', [], '']
  step.value = w.firstStep(decisions.value)
}, { immediate: true })
watch([picks, text], () => open.value && w.writePicks(key(), { clientKey: clientKey.value, picks: picks.value, text: text.value }))

async function send() {
  const empty = w.emptyOther(decisions.value, picks.value)
  if (empty >= 0) {
    [step.value, error.value] = [empty, 'Write your answer under Other, or pick an option.']
    return
  }
  [busy.value, error.value] = [true, '']
  try {
    await postPlanChat(props.namespace, props.name, props.plan,
      { body: text.value.trim(), clientKey: clientKey.value, kind: 'message', decisions: w.answersFrom(decisions.value, picks.value) })
    w.writePicks(key(), null)
    open.value = false
    emit('sent')
  } catch (e) {
    if (e instanceof DecisionsChanged) await reload()
    else error.value = chatPostError(e)
  } finally {
    busy.value = false
  }
}
// reload after a 409: keep the picks of unchanged versions, mark the rest.
async function reload() {
  try {
    const v = await getPlan(props.namespace, props.name, props.plan)
    emit('view', v)
    marked.value = w.changedIds(decisions.value, v.decisions ?? [])
    take(v.decisions ?? [], picks.value)
    step.value = Math.max(0, decisions.value.findIndex((x) => marked.value.includes(x.id)))
    error.value = 'The decisions changed; check the marked steps'
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}
</script>

<template>
  <UModal v-model:open="open" :title="w.wizardButton(decisions).label" :fullscreen="fullscreen"
    :dismissible="!busy" :ui="{ overlay: 'z-40', content: 'z-40 tablet:max-w-2xl', body: 'min-w-0' }">
    <template #body>
      <div class="flex min-w-0 flex-col gap-3 text-sm" data-plan-wizard>
        <nav class="flex flex-wrap gap-1" aria-label="Steps">
          <UButton v-for="(x, i) in decisions" :key="x.id" class="min-h-11 min-w-11 justify-center" size="sm" :label="x.id"
            :color="marked.includes(x.id) ? 'warning' : 'neutral'" :variant="step === i ? 'solid' : 'outline'"
            :aria-current="step === i ? 'step' : undefined" data-wizard-step @click="step = i" />
          <UButton class="min-h-11" size="sm" color="neutral" :variant="d ? 'outline' : 'solid'" label="Summary"
            :aria-current="d ? undefined : 'step'" @click="step = decisions.length" />
        </nav>
        <UAlert v-if="error" role="alert" data-wizard-error :color="alertColor('warning')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" :description="error" />
        <section v-if="d" :key="d.id" class="flex min-w-0 flex-col gap-2" data-wizard-decision>
          <span class="text-(--text-3)" data-wizard-position>{{ step + 1 }} of {{ decisions.length }}</span>
          <h3 class="m-0 text-base [overflow-wrap:anywhere]"><Markdown :text="d.title" mode="inline" /> <span class="text-(--text-3)">{{ d.id }}</span></h3>
          <Markdown :text="d.question" />
          <div v-if="d.why" class="text-(--text-2)"><strong>Why</strong> <Markdown :text="d.why" /></div>
          <fieldset class="m-0 flex min-w-0 flex-col gap-1 border-0 p-0">
            <legend class="sr-only">Your answer</legend>
            <label v-for="(o, i) in d.options" :key="i" class="flex min-h-11 min-w-0 cursor-pointer items-center gap-2 rounded-(--radius-sm) bg-(--surface-2) px-2.5"
              data-wizard-option>
              <input type="radio" :name="`pick-${d.id}`" class="m-0 size-4 flex-none" :checked="pick === i" @change="setPick(i)" />
              <Markdown class="min-w-0 flex-1 [overflow-wrap:anywhere]" :text="o" mode="inline" />
              <Badge v-if="pre?.index === i" class="flex-none" :tone="pre.mark === 'Signed off' ? 'success' : 'info'" data-wizard-mark>{{ pre.mark }}</Badge>
            </label>
            <label class="flex min-h-11 cursor-pointer items-center gap-2 rounded-(--radius-sm) bg-(--surface-2) px-2.5">
              <input type="radio" :name="`pick-${d.id}`" class="m-0 size-4 flex-none" :checked="typeof pick === 'string'" @change="setPick('')" />
              Other
            </label>
            <UTextarea v-if="typeof pick === 'string'" :model-value="pick" :maxlength="w.maxOther" :rows="3" autoresize
              aria-label="Your answer" :ui="{ root: 'w-full', base: 'text-base tablet:text-sm' }" @update:model-value="(t: string) => setPick(t)" />
          </fieldset>
        </section>
        <section v-else class="flex min-w-0 flex-col gap-2" data-wizard-summary>
          <strong>{{ sum.kept.length }} kept, {{ sum.changed.length }} changed</strong>
          <ul class="m-0 flex list-none flex-col gap-1 p-0">
            <li v-for="r in sum.changed" :key="r.id" class="[overflow-wrap:anywhere]" data-wizard-changed>
              {{ r.id }} {{ r.title }}: changed to <Markdown :text="r.pick" mode="inline" />
            </li>
            <li v-for="r in sum.kept" :key="r.id" class="text-(--text-2) [overflow-wrap:anywhere]" data-wizard-kept>{{ r.id }} {{ r.title }}: kept</li>
          </ul>
          <UAlert v-if="view.draftBy === 'owner'" data-wizard-owner-edit :color="alertColor('warning')" variant="subtle" class="mt-3" description="You edited the draft since the agent's last turn; the agent gets your edit." />
          <UTextarea v-model="text" :rows="3" autoresize placeholder="More for the agent (optional)" aria-label="More for the agent"
            :ui="{ root: 'w-full', base: 'text-base tablet:text-sm' }" />
          <span v-if="blocked" class="text-(--text-2)">{{ blocked }}</span>
        </section>
      </div>
    </template>
    <template #footer>
      <UButton class="min-h-11" color="neutral" variant="outline" label="Back" :disabled="step === 0" @click="step--" />
      <UButton v-if="d" class="ml-auto min-h-11" label="Next" @click="step++" />
      <UButton v-else class="ml-auto min-h-11" :label="busy ? '…' : 'Send'" :disabled="busy || !!blocked" data-wizard-send @click="send" />
    </template>
  </UModal>
</template>
