<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UFormField from '@nuxt/ui/components/FormField.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import USelect from '@nuxt/ui/components/Select.vue'
import UTextarea from '@nuxt/ui/components/Textarea.vue'
import { computed, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, post } from '../api'
import { useResource } from '../live'
import type { ResearchForm } from '../types'

// The one-off research form. Nuxt UI fields (docs/ui.md, "Research list and
// form"): inputs are 16 px and controls 44 px high on phones, the fields
// stack there; a server error about a field shows on that field.

// The server's limits (internal/ui/oneoff.go); it checks them again.
const maxPrompt = 16384
const maxTopics = 20
const timeouts: Record<string, string> = { quick: '20 min', deep: '2 h' }
// The fields the server may name in a 400 (oneoff.go, fieldError); an error
// about another field shows next to the submit button.
const fields = ['prompt', 'depth', 'view', 'topics', 'project', 'agent'] as const
type Field = (typeof fields)[number]

// A select's item may not have the value "" (Reka UI), so "auto" and
// "default" stand for no choice.
const auto = 'auto'
const defaultEffort = 'default'

const router = useRouter()
const { data: form, error: loadError } = useResource<ResearchForm>(() => '/research/form', ['project'])

const prompt = ref('')
const depth = ref<'quick' | 'deep'>('quick')
const view = ref<'report' | 'newsflash'>('report')
const topicsText = ref('')
const project = ref('')
// agent is auto, else the index of the project's model.
const agent = ref(auto)
const effort = ref(defaultEffort)

const errors = reactive<Partial<Record<Field, string>>>({})
const formError = ref<string | null>(null)
const sending = ref(false)

// The server counts characters (code points) of the trimmed question.
const promptLength = computed(() => [...prompt.value.trim()].length)
const topics = computed(() =>
  topicsText.value
    .split(',')
    .map((t) => t.trim())
    .filter((t) => t !== ''),
)
const selected = computed(() => form.value?.projects.find((p) => p.name === project.value))
const model = computed(() => (agent.value === auto ? undefined : selected.value?.models[Number(agent.value)]))

const viewItems = computed(() => (form.value?.views ?? []).map((v) => ({ value: v, label: v === 'newsflash' ? 'News flash' : 'Report' })))
const projectItems = computed(() =>
  (form.value?.projects ?? []).map((p) => ({
    value: p.name,
    label: p.available ? p.name : `${p.name} (unavailable)`,
    disabled: !p.available,
  })),
)
const agentItems = computed(() => [
  { value: auto, label: 'auto (chosen by depth and usage)' },
  ...(selected.value?.models ?? []).map((m, i) => ({ value: String(i), label: m.label })),
])
const effortItems = computed(() => [
  { value: defaultEffort, label: 'default effort' },
  ...(form.value?.efforts ?? []).map((e) => ({ value: e, label: e })),
])

// Preselect the first project that can run, once the form has loaded.
watch(form, (f) => {
  if (f && !selected.value) project.value = (f.projects.find((p) => p.available) ?? f.projects[0])?.name ?? ''
})
watch(project, () => {
  agent.value = auto
  effort.value = defaultEffort
})

const canSubmit = computed(
  () => !sending.value && promptLength.value > 0 && promptLength.value <= maxPrompt && !!selected.value?.available,
)

async function submit() {
  if (!canSubmit.value) return
  for (const k of fields) delete errors[k]
  formError.value = null
  sending.value = true
  const m = model.value
  try {
    const created = await post<{ namespace: string; name: string }>('/researches', {
      project: project.value,
      prompt: prompt.value,
      depth: depth.value,
      view: view.value,
      topics: topics.value,
      agent: m && {
        engine: m.engine,
        model: m.model,
        ...(m.engine === 'claude' && effort.value !== defaultEffort ? { effort: effort.value } : {}),
      },
    })
    await router.push(`/research/${encodeURIComponent(created.namespace)}/${encodeURIComponent(created.name)}`)
  } catch (e) {
    if (e instanceof ApiError && (fields as readonly string[]).includes(e.field ?? '')) errors[e.field as Field] = e.message
    else formError.value = e instanceof Error ? e.message : String(e)
  } finally {
    sending.value = false
  }
}

// Shared looks: 16 px and 44 px on phones (style.css's phone rule does the
// same; these keep it explicit), the app's 14 px from a tablet on.
const fieldUI = { label: 'font-semibold text-[13px]', hint: 'text-[13px]', error: 'text-[13px] [overflow-wrap:anywhere]', help: 'text-[13px]' }
const control = 'text-base/5 tablet:text-sm/5'
const selectProps = {
  trailingIcon: 'i-lucide-chevron-down',
  selectedIcon: 'i-lucide-check',
  class: 'min-h-11 w-full tablet:min-h-0 tablet:w-auto tablet:min-w-56',
  ui: { base: control, item: 'min-h-11 tablet:min-h-0' },
} as const
</script>

<template>
  <div class="page">
    <h1>New research</h1>

    <div v-if="loadError" class="banner danger [overflow-wrap:anywhere]">{{ loadError }}</div>
    <div v-if="!form && !loadError" class="card empty">Loading…</div>

    <form
      v-if="form"
      class="card mt-[18px] flex max-w-[820px] min-w-0 flex-col gap-[18px] px-4 py-4 tablet:px-5 tablet:py-[18px]"
      data-research-form
      novalidate
      @submit.prevent="submit"
    >
      <UFormField label="Question" name="prompt" :error="errors.prompt" :ui="fieldUI">
        <UTextarea
          v-model="prompt"
          :rows="6"
          placeholder="What do you want to know? The agent searches the web and answers with a report."
          class="w-full"
          :ui="{ base: `${control} resize-y` }"
          @keydown.meta.enter="submit"
          @keydown.ctrl.enter="submit"
        />
        <template #hint>
          <span :class="promptLength > maxPrompt ? 'text-(--tone-danger-fg)' : 'text-(--text-3)'" data-counter>
            {{ promptLength }} / {{ maxPrompt }}
          </span>
        </template>
      </UFormField>

      <fieldset class="m-0 flex min-w-0 flex-col gap-1.5 border-0 p-0" data-field="depth">
        <legend class="mb-1.5 p-0 text-[13px] font-semibold">Depth</legend>
        <div class="flex flex-col tablet:flex-row tablet:flex-wrap tablet:gap-x-3.5">
          <!-- The label is the 44 px hit area on phones; the radio keeps its size. -->
          <label v-for="d in form.depths" :key="d" class="inline-flex min-h-11 cursor-pointer items-center gap-1.5 tablet:min-h-0">
            <input v-model="depth" type="radio" name="depth" :value="d" class="m-0 size-4" />
            {{ d }} <span class="text-(--text-3)">(up to {{ timeouts[d] ?? '?' }})</span>
          </label>
        </div>
        <p v-if="errors.depth" class="m-0 text-[13px] text-(--tone-danger-fg) [overflow-wrap:anywhere]" role="alert">{{ errors.depth }}</p>
      </fieldset>

      <UFormField label="View" name="view" :error="errors.view" :ui="fieldUI">
        <USelect v-model="view" :items="viewItems" v-bind="selectProps" />
      </UFormField>

      <UFormField
        label="Topics"
        name="topics"
        hint="optional, separated by commas"
        :error="errors.topics || (topics.length > maxTopics ? `At most ${maxTopics} topics.` : undefined)"
        :ui="fieldUI"
      >
        <UInput v-model="topicsText" placeholder="e.g. Kubernetes, Postgres" autocomplete="off" class="w-full" :ui="{ base: control }" />
      </UFormField>

      <UFormField v-if="form.projects.length > 1" label="Project" name="project" :error="errors.project" :ui="fieldUI">
        <USelect v-model="project" :items="projectItems" v-bind="selectProps" />
        <template v-if="form.projects.some((p) => !p.available)" #help>
          Projects without a writer login (<code>spec.research.writerSecret</code>) cannot store results.
        </template>
      </UFormField>
      <div v-else-if="form.projects.length === 0" class="banner warning [overflow-wrap:anywhere]">
        There is no AgentProject in <code>{{ form.namespace }}</code> to run researches with.
      </div>
      <div v-else-if="selected && !selected.available" class="banner warning [overflow-wrap:anywhere]">
        The project {{ selected.name }} cannot run researches: {{ selected.reason }}.
      </div>
      <p v-else-if="errors.project" class="m-0 text-[13px] text-(--tone-danger-fg) [overflow-wrap:anywhere]" role="alert">
        {{ errors.project }}
      </p>

      <div class="flex min-w-0 flex-col gap-[18px] tablet:flex-row tablet:flex-wrap tablet:gap-3">
        <UFormField label="Agent" name="agent" :error="errors.agent" :ui="fieldUI" class="min-w-0">
          <USelect v-model="agent" :items="agentItems" v-bind="selectProps" />
        </UFormField>
        <UFormField v-if="model?.engine === 'claude'" label="Effort" name="effort" :ui="fieldUI" class="min-w-0">
          <USelect v-model="effort" :items="effortItems" v-bind="selectProps" />
        </UFormField>
      </div>

      <div class="flex flex-col gap-3 tablet:flex-row tablet:flex-wrap tablet:items-center">
        <p v-if="formError" class="m-0 text-[13px] text-(--tone-danger-fg) [overflow-wrap:anywhere]" role="alert">{{ formError }}</p>
        <span class="text-(--text-3) tablet:ms-auto">Runs in <code>{{ form.namespace }}</code></span>
        <UButton
          type="submit"
          :disabled="!canSubmit"
          :label="sending ? 'Starting…' : 'Start research'"
          class="min-h-11 justify-center tablet:min-h-0"
        />
      </div>
    </form>
  </div>
</template>
