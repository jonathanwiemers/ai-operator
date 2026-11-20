<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
// Create milestone from a plan (R12, Decisions 8, 10): title, final draft, Spec / Autopilot, the appended lines and the estimate.
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { createPlanMilestone, getEstimate } from '../client'
import { estimateLabel } from '../estimate'
import { useRoute } from 'vue-router'
import { createWarning } from '../planDecisions'
import { planTabs, tabQuery } from '../streamTabs'
import { planHeaderLines, planMilestoneTitle } from '../planRows'
import UButton from '@nuxt/ui/components/Button.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import USwitch from '@nuxt/ui/components/Switch.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import ExtLink from './ExtLink.vue'
import type { FollowUpMilestoneView } from '../generated/views'
import { useBackClose } from '../ui/useBackClose'

const UModal = defineAsyncComponent(() => import('@nuxt/ui/components/Modal.vue'))
const props = defineProps<{ namespace: string; name: string; plan: string; title: string; draft: string; pending?: number }>()
const route = useRoute()
const warning = computed(() => createWarning(props.pending ?? 0))
const emit = defineEmits<{ created: [] }>()
const open = defineModel<boolean>('open', { required: true })
const [title, text, spec, autopilot, estimate, error, busy] = [ref(''), ref(''), ref(true), ref(true), ref(''), ref(''), ref(false)]
const created = ref<FollowUpMilestoneView | null>(null)
useBackClose(open)
watch(open, async (o) => {
  if (!o) return
  [title.value, text.value, error.value, created.value, estimate.value] = [planMilestoneTitle(props.title, props.draft), props.draft, '', null, '…']
  estimate.value = await getEstimate(props.namespace, props.name, { plan: props.plan }).then(estimateLabel, (e) => (e instanceof Error ? e.message : String(e)))
}, { immediate: true })
async function create() {
  [busy.value, error.value] = [true, '']
  try {
    created.value = await createPlanMilestone(props.namespace, props.name, props.plan,
      { title: title.value.trim(), description: text.value.trim(), spec: spec.value, autopilot: autopilot.value })
    emit('created')
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" title="Create milestone" :dismissible="!busy" :ui="{ overlay: 'z-40', content: 'z-40 tablet:max-w-3xl' }">
    <template #body>
      <UAlert v-if="created" role="status" data-plan-created :color="alertColor('info')" variant="subtle" class="mt-3">
        <template #description>
        Created the milestone %{{ created.iid }}: <ExtLink :href="created.webURL">on GitLab</ExtLink>.
        </template>
      </UAlert>
      <div v-else class="flex flex-col gap-2 text-sm" data-plan-create>
        <UAlert v-if="warning" role="status" data-create-warning :color="alertColor('warning')" variant="subtle" class="mt-3">
          <template #description>
          {{ warning }}.
          <RouterLink :to="{ query: tabQuery(route.query, 'decisions', planTabs) }" @click="open = false">Review them</RouterLink>
          </template>
        </UAlert>
        <UInput v-model="title" :maxlength="255" aria-label="Title" placeholder="Title" />
        <UTextarea v-model="text" :rows="14" spellcheck="true" aria-label="Description" :ui="{ base: 'font-mono text-base/5 tablet:text-[13px]/5' }" />
        <div class="flex gap-4"><USwitch v-model="spec" label="Spec" /><USwitch v-model="autopilot" label="Autopilot" /></div>
        <pre class="m-0 text-xs text-(--text-2)" aria-label="Lines the operator appends">{{ planHeaderLines(spec, autopilot).join('\n') }}</pre>
        <span data-estimate>Estimate: {{ estimate }}</span>
        <UAlert v-if="error" role="alert" :color="alertColor('danger')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" :description="error" />
      </div>
    </template>
    <template #footer>
      <UButton color="neutral" variant="outline" :label="created ? 'Close' : 'Cancel'" :disabled="busy" @click="open = false" />
      <UButton v-if="!created" :label="busy ? '…' : 'Create milestone'" :disabled="busy || !title.trim() || !text.trim()" @click="create" />
    </template>
  </UModal>
</template>
