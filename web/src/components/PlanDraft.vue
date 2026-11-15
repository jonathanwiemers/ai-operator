<script setup lang="ts">
// Plan draft pane (R11, Decision 5): Edit / Preview, explicit Save, and the
// agent's newer draft offered as a diff while the owner has unsaved edits.
import { ref, watch } from 'vue'
import { savePlanDraft } from '../client'
import { DraftConflict, keepMine, onServerDraft, readDraftEdit, takeAgent, writeDraftEdit, type DiffLine } from '../planDraft'
import UButton from '@nuxt/ui/components/Button.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import Markdown from './Markdown.vue'
import type { PlanView } from '../generated/views'

const props = defineProps<{ namespace: string; name: string; plan: string; view: PlanView; canEdit: boolean }>()
const emit = defineEmits<{ view: [PlanView] }>()
const editor = ref(readDraftEdit(props.namespace, props.plan) ?? takeAgent(props.view))
const [mode, conflict, error] = [ref(editor.value.dirty ? 'edit' : 'preview'), ref<DiffLine[] | null>(null), ref('')]
watch(editor, (s) => writeDraftEdit(props.namespace, props.plan, s))
watch(() => props.view, (v) => {
  const r = onServerDraft(editor.value, v)
  if (r.action === 'replace') [editor.value, conflict.value] = [takeAgent(v), null]
  else if (r.action === 'conflict') conflict.value = r.diff
}, { immediate: true })
const resolve = (agent: boolean) => {
  editor.value = agent ? takeAgent(props.view) : keepMine(editor.value, props.view)
  conflict.value = null
}
async function save() {
  try {
    const v = await savePlanDraft(props.namespace, props.name, props.plan, { baseRevision: editor.value.base, draft: editor.value.text })
    editor.value = takeAgent(v)
    emit('view', v)
  } catch (e) {
    if (e instanceof DraftConflict) emit('view', e.view) // the watch above shows the newer draft
    else error.value = e instanceof Error ? e.message : String(e)
  }
}
</script>

<template>
  <div class="card flex flex-col gap-2 px-4 py-3.5" data-plan-draft>
    <div v-if="canEdit" class="flex items-center gap-1.5">
      <UButton v-for="m in ['edit', 'preview']" :key="m" size="sm" color="neutral" :variant="mode === m ? 'solid' : 'ghost'"
        :label="m === 'edit' ? 'Edit' : 'Preview'" @click="mode = m" />
      <UButton class="ml-auto" size="sm" label="Save" :disabled="!editor.dirty" @click="save" />
    </div>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="canEdit && conflict" class="banner" data-newer-draft>
      <strong>The agent has a newer draft</strong> (+ the agent's lines, − yours)
      <pre class="my-2 max-h-80 overflow-auto text-xs"><div v-for="(l, i) in conflict" :key="i" :class="{ 'bg-(--tone-success-bg)': l.kind === 'add', 'bg-(--tone-danger-bg)': l.kind === 'del' }">{{ { add: '+', del: '−', same: ' ' }[l.kind] }} {{ l.text }}</div></pre>
      <UButton size="sm" label="Use agent's draft" @click="resolve(true)" />
      <UButton class="ml-1.5" size="sm" color="neutral" variant="outline" label="Keep mine" @click="resolve(false)" />
    </div>
    <UTextarea v-if="canEdit && mode === 'edit'" :model-value="editor.text" :rows="20" autoresize spellcheck="true"
      :ui="{ root: 'w-full', base: 'font-mono text-base/5 tablet:text-[13px]/5' }" aria-label="Draft"
      @update:model-value="(t: string) => (editor = { ...editor, text: t, dirty: true })" />
    <Markdown v-else :text="(canEdit ? editor.text : view.draft) || 'No draft yet.'" />
  </div>
</template>
