<script setup lang="ts">
// Create all (R8, Decision 11): one Spec / Autopilot choice for every
// uncreated draft, listed read-only in order with its title and estimate;
// Create all creates them one by one with their saved text and stops at the
// first error. Closing it after a creation emits created (the page reloads).
import { computed, defineAsyncComponent, reactive, ref, watch } from 'vue'
import UAlert from '@nuxt/ui/components/Alert.vue'
import UBadge from '@nuxt/ui/components/Badge.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import USwitch from '@nuxt/ui/components/Switch.vue'
import { alertColor } from '../badgeTone'
import { createPlanMilestone, getEstimate } from '../client'
import { estimateLabel } from '../estimate'
import { createAll, uncreated, type CreateAllResult } from '../planCreateAll'
import { createWarning } from '../planDecisions'
import { draftLabel } from '../planDraft'
import { planHeaderLines, planMilestoneTitle } from '../planRows'
import { announce } from '../ui/useAnnounce'
import { useBackClose } from '../ui/useBackClose'
import ExtLink from './ExtLink.vue'
import type { PlanDraftView } from '../generated/views'

const UModal = defineAsyncComponent(() => import('@nuxt/ui/components/Modal.vue'))
const props = defineProps<{ namespace: string; name: string; plan: string; drafts: PlanDraftView[]; pending?: number }>()
const emit = defineEmits<{ created: [] }>()
const open = defineModel<boolean>('open', { required: true })
useBackClose(open)
const warning = computed(() => createWarning(props.pending ?? 0))
type Row = { key: number; label: string; title: string; draft: string; estimate: string; state: 'waiting' | 'creating' | 'created' | 'failed'; iid?: number; url?: string; error?: string }
const rows = ref<Row[]>([])
const [spec, autopilot, busy] = [ref(true), ref(true), ref(false)]
const result = ref<CreateAllResult | null>(null)
// The rows are taken when the dialog opens, so a reload behind it does not
// change the list it creates.
watch(open, (o) => {
  if (!o) {
    if (result.value?.created.length) emit('created')
    return
  }
  result.value = null
  rows.value = uncreated(props.drafts).map((d) => reactive<Row>({
    key: d.key, label: draftLabel(d), title: planMilestoneTitle(d.title, d.draft), draft: d.draft, estimate: '…', state: 'waiting',
  }))
  for (const r of rows.value) {
    void getEstimate(props.namespace, props.name, { plan: props.plan, draft: r.key })
      .then(estimateLabel, (e) => (e instanceof Error ? e.message : String(e))).then((s) => (r.estimate = s))
  }
}, { immediate: true })
const ready = computed(() => rows.value.every((r) => r.title.trim() && r.draft.trim()))
const failed = computed(() => rows.value.find((r) => r.key === result.value?.failed?.key))
const later = computed(() => rows.value.filter((r) => result.value?.skipped.includes(r.key)).map((r) => r.label))

async function run() {
  busy.value = true
  announce(`Creating ${rows.value.length} milestones.`)
  result.value = await createAll(rows.value, async (r) => {
    r.state = 'creating'
    try {
      const m = await createPlanMilestone(props.namespace, props.name, props.plan,
        { key: r.key, title: r.title.trim(), description: r.draft.trim(), spec: spec.value, autopilot: autopilot.value })
      Object.assign(r, { state: 'created', iid: m.iid, url: m.webURL })
      return m
    } catch (e) {
      Object.assign(r, { state: 'failed', error: e instanceof Error ? e.message : String(e) })
      throw e
    }
  })
  busy.value = false
  const made = result.value.created.map((c) => `%${c.iid}`).join(', ')
  if (result.value.failed) announce(`${failed.value?.label} failed: ${result.value.failed.message}`, { assertive: true })
  else announce(`Created ${made}.`)
}
const stateLabel = (r: Row) => ({ waiting: 'waiting', creating: 'creating…', created: 'created', failed: 'failed' })[r.state]
</script>

<template>
  <UModal v-model:open="open" title="Create all milestones" :dismissible="!busy" :ui="{ overlay: 'z-40', content: 'z-40 tablet:max-w-3xl' }">
    <template #body>
      <div class="flex flex-col gap-2 text-sm" data-plan-create-all>
        <UAlert v-if="warning && !result" role="status" :color="alertColor('warning')" variant="subtle" :description="`${warning}.`" />
        <p class="m-0">The drafts are created in this order, each with its saved text.</p>
        <ol class="m-0 flex list-decimal flex-col gap-1.5 pl-6" aria-label="Drafts to create">
          <li v-for="r in rows" :key="r.key" :data-create-all-row="r.key">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium">{{ r.title || r.label }}</span>
              <ExtLink v-if="r.state === 'created'" :href="r.url">%{{ r.iid }}</ExtLink>
              <UBadge v-else :color="r.state === 'failed' ? 'error' : 'neutral'" variant="subtle" size="sm" :label="stateLabel(r)" />
            </div>
            <div class="text-xs text-(--text-2)" data-estimate>Estimate: {{ r.estimate }}</div>
            <div v-if="r.error" class="text-xs text-(--tone-danger-fg) [overflow-wrap:anywhere]">{{ r.error }}</div>
          </li>
        </ol>
        <div class="flex gap-4"><USwitch v-model="spec" label="Spec" :disabled="busy || !!result" /><USwitch v-model="autopilot" label="Autopilot" :disabled="busy || !!result" /></div>
        <pre class="m-0 text-xs text-(--text-2)" aria-label="Lines the operator appends to each">{{ planHeaderLines(spec, autopilot).join('\n') }}</pre>
        <UAlert v-if="!ready" role="status" :color="alertColor('warning')" variant="subtle" description="Every draft needs a title and a text: save them first." />
        <UAlert v-if="result?.failed" role="alert" data-create-all-failed :color="alertColor('danger')" variant="subtle" class="[overflow-wrap:anywhere]"
          :title="`${failed?.label} failed`" :description="result.failed.message + (later.length ? ` ${later.join(', ')} stay${later.length === 1 ? 's' : ''} a draft.` : '')" />
      </div>
    </template>
    <template #footer>
      <UButton color="neutral" variant="outline" :label="result ? 'Close' : 'Cancel'" :disabled="busy" @click="open = false" />
      <UButton v-if="!result" :label="busy ? '…' : 'Create all'" :disabled="busy || !ready || !rows.length" @click="run" />
    </template>
  </UModal>
</template>
