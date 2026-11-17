<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import PlanRow from './PlanRow.vue'
import { getPlans, LoginRequiredError, NotFoundError } from '../client'
import { planSections } from '../planRows'
import type { PlanOverview } from '../generated/views'

// The plan list (decision 12) of every visible project, or of one project:
// Drafting, then Milestones created, each hidden when empty. It loads on
// mount and when the project changes, not live. projects (with steer) stays
// in state for the Plan milestone button. notFound: no chat, or a project
// the user may not view.
const props = defineProps<{ project?: { namespace: string; name: string } }>()
const emit = defineEmits<{ notFound: [] }>()

const overview = ref<PlanOverview | null>(null)
const error = ref('')
const projects = computed(() => overview.value?.projects ?? [])
const sections = computed(() => planSections(overview.value?.plans ?? []))
let seq = 0

async function load() {
  const n = ++seq
  overview.value = null
  error.value = ''
  try {
    const res = await getPlans(props.project)
    if (n === seq) overview.value = res
  } catch (e) {
    if (n !== seq || e instanceof LoginRequiredError) return
    if (e instanceof NotFoundError) emit('notFound')
    else error.value = e instanceof Error ? e.message : String(e)
  }
}
// The key, not the object: a host's inline {namespace, name} is new on
// every render.
watch(() => (props.project ? `${props.project.namespace}/${props.project.name}` : ''), load, { immediate: true })

defineExpose({ projects })
</script>

<template>
  <div data-plan-list>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <p v-else-if="!overview" class="muted">Loading…</p>
    <p v-else-if="!overview.plans.length" class="muted">No plans yet.</p>
    <template v-else>
      <section v-if="sections.drafting.length" class="section">
        <h2 class="text-[15px]">Drafting</h2>
        <ul class="card m-0 list-none overflow-hidden p-0" aria-label="Drafting">
          <PlanRow v-for="p in sections.drafting" :key="`${p.namespace}/${p.name}`" :item="p" :show-project="!project" />
        </ul>
      </section>
      <section v-if="sections.created.length" class="section">
        <h2 class="text-[15px]">Milestones created</h2>
        <ul class="card m-0 list-none overflow-hidden p-0" aria-label="Milestones created">
          <PlanRow v-for="p in sections.created" :key="`${p.namespace}/${p.name}`" :item="p" :show-project="!project" />
        </ul>
      </section>
    </template>
  </div>
</template>
