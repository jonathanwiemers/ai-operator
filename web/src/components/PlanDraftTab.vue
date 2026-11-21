<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
// One draft of a plan (R8, Decision 10): Edit / Preview, explicit Save, the
// agent's newer draft offered as a diff while the owner has unsaved edits,
// Remove and Move left / right, and Create milestone on the next draft only.
// A created draft is read-only, with its milestone's link.
import { ref, watch } from 'vue'
import { deletePlanDraft, orderPlanDrafts, savePlanDraft } from '../client'
import { DraftConflict, draftLabel, keepMine, movedKeys, onServerDraft, readDraftEdit, takeAgent, writeDraftEdit, type DiffLine } from '../planDraft'
import UButton from '@nuxt/ui/components/Button.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import ExtLink from './ExtLink.vue'
import Markdown from './Markdown.vue'
import type { PlanDraftView, PlanView } from '../generated/views'

const props = defineProps<{
  namespace: string; name: string; plan: string; view: PlanView; draft: PlanDraftView
  title: string; canEdit: boolean; canCreate: boolean; canLeft: boolean; canRight: boolean
}>()
// create asks PlanDraft.vue for the create dialog of the saved draft: the
// dialog lives there, outside the tabs, so it stays open when this tab goes
// (key 0 saved as a new draft, or the next draft moving on).
const emit = defineEmits<{ view: [PlanView]; create: [{ key: number; title: string; draft: string }] }>()
const key = props.draft.key
const editor = ref(readDraftEdit(props.namespace, props.plan, key) ?? takeAgent(props.draft))
const [mode, conflict, error] = [ref(editor.value.dirty ? 'edit' : 'preview'), ref<DiffLine[] | null>(null), ref('')]
watch(editor, (s) => writeDraftEdit(props.namespace, props.plan, key, s))
watch(() => props.draft, (d) => {
  const r = onServerDraft(editor.value, d)
  if (r.action === 'replace') [editor.value, conflict.value] = [takeAgent(d), null]
  else if (r.action === 'conflict') conflict.value = r.diff
}, { immediate: true })
const resolve = (agent: boolean) => {
  editor.value = agent ? takeAgent(props.draft) : keepMine(editor.value, props.draft)
  conflict.value = null
}
// Create milestone saves unsaved edits first; on a 409 the newer draft shows
// instead. The ask goes out before the saved view, while this tab is mounted.
const ask = (d: Pick<PlanDraftView, 'key' | 'draft'>) => emit('create', { key: d.key, title: props.title, draft: d.draft })
const create = () => (editor.value.dirty ? void save(ask) : ask({ key, draft: editor.value.text }))

// change runs a change of the draft list and shows its view; a 409 with the
// server's view shows that one (the watch above offers a newer draft).
async function change(run: () => Promise<PlanView>): Promise<boolean> {
  error.value = ''
  try {
    emit('view', await run())
    return true
  } catch (e) {
    if (e instanceof DraftConflict) emit('view', e.view)
    else error.value = e instanceof Error ? e.message : String(e)
    return false
  }
}
async function save(saved?: (d: PlanDraftView) => void): Promise<boolean> {
  const body = { key, baseRevision: editor.value.base, draft: editor.value.text }
  return change(async () => {
    const v = await savePlanDraft(props.namespace, props.name, props.plan, body)
    const d = v.drafts.find((d) => d.key === key) ?? v.drafts.at(-1) // key 0: the new draft
    if (d) {
      editor.value = takeAgent(d)
      saved?.(d)
    }
    return v
  })
}
const move = (dir: -1 | 1) => change(() => orderPlanDrafts(props.namespace, props.name, props.plan, movedKeys(props.view.drafts, key, dir)))
function remove() {
  if (!window.confirm(`Remove ${draftLabel(props.draft)}? Its text is lost.`)) return
  void change(async () => {
    const v = await deletePlanDraft(props.namespace, props.name, props.plan, key)
    editor.value = { ...editor.value, dirty: false } // drops the stored edit
    return v
  })
}
</script>

<template>
  <div class="flex flex-col gap-2" :data-plan-draft-key="key">
    <p v-if="draft.createdMilestoneIID" class="m-0 text-sm" data-draft-created>
      Created as <ExtLink :href="draft.createdMilestoneURL">%{{ draft.createdMilestoneIID }}</ExtLink>; read-only.
    </p>
    <p v-else-if="draft.creating" class="m-0 text-sm" data-draft-created>Its milestone is being created; read-only.</p>
    <div v-if="canEdit || $slots.actions" class="flex flex-wrap items-center gap-1.5">
      <template v-if="canEdit">
        <UButton v-for="m in ['edit', 'preview']" :key="m" size="sm" color="neutral" :variant="mode === m ? 'solid' : 'ghost'"
          :label="m === 'edit' ? 'Edit' : 'Preview'" @click="mode = m" />
        <UButton v-if="canLeft" size="sm" color="neutral" variant="ghost" icon="i-lucide-chevron-left" aria-label="Move left" @click="move(-1)" />
        <UButton v-if="canRight" size="sm" color="neutral" variant="ghost" icon="i-lucide-chevron-right" aria-label="Move right" @click="move(1)" />
        <UButton v-if="draft.key" size="sm" color="error" variant="ghost" label="Remove" @click="remove" />
        <UButton class="ml-auto" size="sm" color="neutral" variant="outline" label="Save" :disabled="!editor.dirty" @click="save()" />
      </template>
      <UButton v-if="canCreate" size="sm" label="Create milestone" :disabled="!editor.text.trim()" @click="create" />
      <span v-if="!canEdit" class="ml-auto" />
      <slot name="actions" /><!-- the decision wizard's button (R7), for the whole plan -->
    </div>
    <UAlert v-if="error" role="alert" :color="alertColor('danger')" variant="subtle" class="mt-3 [overflow-wrap:anywhere]" :description="error" />
    <UAlert v-if="canEdit && conflict" data-newer-draft :color="alertColor('neutral')" variant="subtle" class="mt-3"
      title="The agent has a newer draft">
      <template #description>
        (+ the agent's lines, − yours)
        <pre class="my-2 max-h-80 overflow-auto text-xs"><div v-for="(l, i) in conflict" :key="i" :class="{ 'bg-(--tone-success-bg)': l.kind === 'add', 'bg-(--tone-danger-bg)': l.kind === 'del' }">{{ { add: '+', del: '−', same: ' ' }[l.kind] }} {{ l.text }}</div></pre>
      </template>
      <template #actions>
        <UButton size="sm" label="Use agent's draft" @click="resolve(true)" />
        <UButton size="sm" color="neutral" variant="outline" label="Keep mine" @click="resolve(false)" />
      </template>
    </UAlert>
    <UTextarea v-if="canEdit && mode === 'edit'" :model-value="editor.text" :rows="20" autoresize spellcheck="true"
      :ui="{ root: 'w-full', base: 'font-mono text-base/5 tablet:text-[13px]/5' }" aria-label="Draft"
      @update:model-value="(t: string) => (editor = { ...editor, text: t, dirty: true })" />
    <Markdown v-else :text="(canEdit ? editor.text : draft.draft) || 'No draft yet.'" />
  </div>
</template>
