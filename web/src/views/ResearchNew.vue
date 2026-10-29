<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, post } from '../api'
import { useResource } from '../live'
import type { ResearchForm } from '../types'

// The server's limits (internal/ui/oneoff.go); it checks them again.
const maxPrompt = 16384
const maxTopics = 20
const timeouts: Record<string, string> = { quick: '20 min', deep: '2 h' }

const router = useRouter()
const { data: form, error: loadError } = useResource<ResearchForm>(() => '/research/form', ['project'])

const prompt = ref('')
const depth = ref<'quick' | 'deep'>('quick')
const view = ref<'report' | 'newsflash'>('report')
const topicsText = ref('')
const project = ref('')
// agent is "" for auto, else the index of the project's model.
const agent = ref('')
const effort = ref('')

const errors = reactive<Record<string, string>>({})
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
const model = computed(() => (agent.value === '' ? undefined : selected.value?.models[Number(agent.value)]))

// Preselect the first project that can run, once the form has loaded.
watch(form, (f) => {
  if (f && !selected.value) project.value = (f.projects.find((p) => p.available) ?? f.projects[0])?.name ?? ''
})
watch(project, () => {
  agent.value = ''
  effort.value = ''
})

const canSubmit = computed(
  () => !sending.value && promptLength.value > 0 && promptLength.value <= maxPrompt && !!selected.value?.available,
)

async function submit() {
  if (!canSubmit.value) return
  for (const k of Object.keys(errors)) delete errors[k]
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
        ...(m.engine === 'claude' && effort.value ? { effort: effort.value } : {}),
      },
    })
    await router.push(`/research/${encodeURIComponent(created.namespace)}/${encodeURIComponent(created.name)}`)
  } catch (e) {
    if (e instanceof ApiError && e.field) errors[e.field] = e.message
    else formError.value = e instanceof Error ? e.message : String(e)
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div class="page">
    <nav class="crumbs muted">
      <RouterLink to="/research">Research</RouterLink>
      <span>/</span>
      <span>New research</span>
    </nav>
    <h1>New research</h1>

    <div v-if="loadError" class="banner danger">{{ loadError }}</div>
    <div v-if="!form && !loadError" class="card empty">Loading…</div>

    <form v-if="form" class="card form" @submit.prevent="submit">
      <label class="field">
        <span class="label">Question</span>
        <textarea
          v-model="prompt"
          rows="6"
          placeholder="What do you want to know? The agent searches the web and answers with a report."
          @keydown.meta.enter="submit"
          @keydown.ctrl.enter="submit"
        />
        <span class="counter faint" :class="{ over: promptLength > maxPrompt }">
          {{ promptLength }} / {{ maxPrompt }}
        </span>
        <span v-if="errors.prompt" class="err">{{ errors.prompt }}</span>
      </label>

      <fieldset class="field">
        <legend class="label">Depth</legend>
        <div class="row">
          <label v-for="d in form.depths" :key="d" class="choice">
            <input v-model="depth" type="radio" name="depth" :value="d" />
            {{ d }} <span class="faint">(up to {{ timeouts[d] ?? '?' }})</span>
          </label>
        </div>
        <span v-if="errors.depth" class="err">{{ errors.depth }}</span>
      </fieldset>

      <label class="field">
        <span class="label">View</span>
        <select v-model="view">
          <option v-for="v in form.views" :key="v" :value="v">{{ v === 'newsflash' ? 'News flash' : 'Report' }}</option>
        </select>
        <span v-if="errors.view" class="err">{{ errors.view }}</span>
      </label>

      <label class="field">
        <span class="label">Topics <span class="faint">(optional, separated by commas)</span></span>
        <input v-model="topicsText" type="text" placeholder="e.g. Kubernetes, Postgres" />
        <span v-if="topics.length > maxTopics" class="err">At most {{ maxTopics }} topics.</span>
        <span v-if="errors.topics" class="err">{{ errors.topics }}</span>
      </label>

      <label v-if="form.projects.length > 1" class="field">
        <span class="label">Project</span>
        <select v-model="project">
          <option v-for="p in form.projects" :key="p.name" :value="p.name" :disabled="!p.available">
            {{ p.name }}{{ p.available ? '' : ' (unavailable)' }}
          </option>
        </select>
        <span v-if="form.projects.some((p) => !p.available)" class="hint faint">
          Projects without a writer login (<code>spec.research.writerSecret</code>) cannot store results.
        </span>
        <span v-if="errors.project" class="err">{{ errors.project }}</span>
      </label>
      <div v-else-if="form.projects.length === 0" class="banner warning">
        There is no AgentProject in <code>{{ form.namespace }}</code> to run researches with.
      </div>
      <div v-else-if="selected && !selected.available" class="banner warning">
        The project {{ selected.name }} cannot run researches: {{ selected.reason }}.
      </div>
      <span v-else-if="errors.project" class="err">{{ errors.project }}</span>

      <div class="field">
        <span class="label">Agent</span>
        <div class="row">
          <select v-model="agent" aria-label="Agent">
            <option value="">auto (chosen by depth and usage)</option>
            <option v-for="(m, i) in selected?.models ?? []" :key="m.label" :value="String(i)">{{ m.label }}</option>
          </select>
          <select v-if="model?.engine === 'claude'" v-model="effort" aria-label="Effort">
            <option value="">default effort</option>
            <option v-for="e in form.efforts" :key="e" :value="e">{{ e }}</option>
          </select>
        </div>
        <span v-if="errors.agent" class="err">{{ errors.agent }}</span>
      </div>

      <div class="row actions">
        <span v-if="formError" class="err">{{ formError }}</span>
        <span class="spacer" />
        <span class="faint">Runs in <code>{{ form.namespace }}</code></span>
        <button type="submit" class="btn primary" :disabled="!canSubmit">
          {{ sending ? 'Starting…' : 'Start research' }}
        </button>
      </div>
    </form>
  </div>
</template>

<style scoped>
.crumbs {
  display: flex;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 14px;
}

.form {
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-width: 820px;
  margin-top: 18px;
  padding: 18px 20px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin: 0;
  padding: 0;
  border: none;
}

.label {
  font-weight: 600;
  font-size: 13px;
}

textarea,
input[type='text'],
select {
  box-sizing: border-box;
  padding: 7px 10px;
  border: 1px solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text);
  font: inherit;
}

textarea {
  width: 100%;
  resize: vertical;
}

select {
  align-self: flex-start;
}

.choice {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin-right: 14px;
}

.counter {
  align-self: flex-end;
  font-size: 12px;
}

.counter.over {
  color: var(--tone-danger-fg);
}

.hint {
  font-size: 13px;
}

.err {
  color: var(--tone-danger-fg);
  font-size: 13px;
}

.actions {
  gap: 12px;
}

.btn:disabled {
  opacity: 0.55;
  cursor: default;
}
</style>
