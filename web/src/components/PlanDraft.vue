<script setup lang="ts">
// Plan drafts (R8): one tab per draft in list order, with its title and,
// once created, its %IID badge; arrow keys move between the tabs. A plan
// without drafts shows one empty tab, saved or created as a new draft.
import { computed, ref, watch } from 'vue'
import UTabs from '@nuxt/ui/components/Tabs.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import { canMove, draftLabel, draftLocked, nextToCreate, readDraftEdit } from '../planDraft'
import { uncreated } from '../planCreateAll'
import PlanCreateAllDialog from './PlanCreateAllDialog.vue'
import PlanCreateDialog from './PlanCreateDialog.vue'
import PlanDraftTab from './PlanDraftTab.vue'
import type { PlanDraftView, PlanView } from '../generated/views'
import type { PlanUploads } from '../attachments'

const props = defineProps<{ namespace: string; name: string; plan: string; view: PlanView; canEdit: boolean; uploads?: PlanUploads }>()
const emit = defineEmits<{ view: [PlanView]; created: [] }>()
const empty: PlanDraftView = { key: 0, title: '', draft: '', revision: 0 }
const drafts = computed(() => (props.view.drafts.length ? props.view.drafts : [empty]))
const next = computed(() => nextToCreate(drafts.value))
const items = computed(() => drafts.value.map((d) => ({
  label: d.key ? draftLabel(d) : 'Draft 1', value: d.key, slot: 'draft' as const, draft: d,
  badge: d.createdMilestoneIID ? `%${d.createdMilestoneIID}` : d.creating ? 'creating' : undefined,
})))
// The open tab is a draft key: the next to create at first, the first draft
// once the open one is gone (removed, or key 0 saved as a new draft).
const active = ref<string | number>(next.value?.key ?? drafts.value[0]!.key)
watch(drafts, (list) => {
  if (!list.some((d) => d.key === active.value)) active.value = (next.value ?? list[list.length - 1]!).key
})
// The create dialog's draft, set by a tab's Create milestone (after its save).
const createFor = ref<{ key: number; title: string; draft: string } | null>(null)
const creating = ref(false)
const openCreate = (d: { key: number; title: string; draft: string }) => [createFor.value, creating.value] = [d, true]
// The dialog's title: the draft's, the plan's for a single draft.
const title = (d: PlanDraftView) => d.title || (drafts.value.length === 1 ? props.view.title : '')
// Create all (Decision 11), with more than one uncreated draft. It creates
// the saved texts, so an unsaved edit in any tab (stored per draft, also for
// tabs not shown) blocks it; edits bumps when a tab's edit changes.
const showAll = computed(() => uncreated(props.view.drafts).length > 1)
const edits = ref(0)
const unsaved = computed(() => {
  void edits.value
  return props.view.drafts.filter((d) => readDraftEdit(props.namespace, props.plan, d.key)).map(draftLabel)
})
const creatingAll = ref(false)
</script>

<template>
  <div class="card flex flex-col gap-2 px-4 py-3.5" data-plan-draft>
    <UTabs v-model="active" :items="items" variant="link" size="sm" aria-label="Drafts" :ui="{ list: 'overflow-x-auto' }">
      <template #draft="{ item }">
        <PlanDraftTab :namespace="namespace" :name="name" :plan="plan" :view="view" :uploads="uploads" :draft="item.draft" :title="title(item.draft)"
          :can-edit="canEdit && !draftLocked(item.draft)" :can-create="canEdit && next?.key === item.draft.key"
          :can-left="canMove(drafts, item.draft.key, -1)" :can-right="canMove(drafts, item.draft.key, 1)"
          @view="(v) => emit('view', v)" @create="openCreate" @edited="edits++">
          <template v-if="canEdit" #actions>
            <template v-if="showAll && next?.key === item.draft.key">
              <UButton size="sm" color="neutral" variant="outline" label="Create all" :disabled="unsaved.length > 0"
                :aria-describedby="unsaved.length ? 'plan-create-all-note' : undefined" data-create-all @click="creatingAll = true" />
              <span v-if="unsaved.length" id="plan-create-all-note" class="text-xs text-(--text-2)">Save {{ unsaved.join(', ') }} first to create all.</span>
            </template>
            <slot name="actions" />
          </template>
        </PlanDraftTab>
      </template>
    </UTabs>
    <PlanCreateDialog v-if="createFor" :key="createFor.key" v-model:open="creating" :namespace="namespace" :name="name" :plan="plan"
      :draft-key="createFor.key" :title="createFor.title" :draft="createFor.draft" :drafts="view.drafts" :pending="view.decisionsPending ?? 0" @created="emit('created')" />
    <PlanCreateAllDialog v-if="creatingAll || showAll" v-model:open="creatingAll" :namespace="namespace" :name="name" :plan="plan"
      :drafts="view.drafts" :pending="view.decisionsPending ?? 0" @created="emit('created')" />
  </div>
</template>
