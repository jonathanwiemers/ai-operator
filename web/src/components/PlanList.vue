<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import UButton from '@nuxt/ui/components/Button.vue'
import PlanRow from './PlanRow.vue'
import { ApiError, createPlan, getPlans, LoginRequiredError, NotFoundError } from '../client'
import { planSections } from '../planRows'
import { planStartProjects } from '../planStart'
import type { PlanOverview, PlanProject } from '../generated/views'

// The plan list (decision 12) of every visible project, or of one project:
// Drafting, then Milestones created, each hidden when empty. It loads on
// mount and when the project changes, not live. notFound: no chat, or a
// project the user may not view.
//
// Plan milestone (decision 14) shows when the caller may steer a listed
// project, also on an empty list. It starts an untitled plan and opens it:
// right away with one such project (always so with project set), after a
// pick from an inline list with more. A 403 hides it until the next load.
// actionsTo: a selector in the host's page header; the button moves there
// (Teleport), the picker stays above the list.
const props = defineProps<{ project?: { namespace: string; name: string }; actionsTo?: string }>()
const emit = defineEmits<{ notFound: [] }>()

const overview = ref<PlanOverview | null>(null)
const error = ref('')
const projects = computed(() => overview.value?.projects ?? [])
const sections = computed(() => planSections(overview.value?.plans ?? []))
const starts = computed(() => planStartProjects(projects.value))
const [busy, picking, forbidden, startError] = [ref(false), ref(false), ref(false), ref('')]
const router = useRouter()
let seq = 0

async function load() {
  const n = ++seq
  overview.value = null
  ;[error.value, startError.value, forbidden.value, picking.value] = ['', '', false, false]
  try {
    const res = await getPlans(props.project)
    if (n === seq) overview.value = res
  } catch (e) {
    if (n !== seq || e instanceof LoginRequiredError) return
    // A host without its own not-found page shows an empty list.
    if (e instanceof NotFoundError) {
      overview.value = { plans: [], projects: [] }
      emit('notFound')
    } else error.value = e instanceof Error ? e.message : String(e)
  }
}
// The key, not the object: a host's inline {namespace, name} is new on
// every render.
watch(() => (props.project ? `${props.project.namespace}/${props.project.name}` : ''), load, { immediate: true })

function onPlan() {
  if (starts.value.length === 1) void start(starts.value[0]!)
  else picking.value = !picking.value
}
async function start(p: PlanProject) {
  if (busy.value) return
  ;[busy.value, startError.value] = [true, '']
  try {
    const plan = await createPlan(p.namespace, p.name)
    await router.push({ name: 'plans', params: { namespace: p.namespace, name: p.name, plan: plan.name } })
  } catch (e) {
    startError.value = e instanceof Error ? e.message : String(e)
    if (e instanceof ApiError && e.status === 403) forbidden.value = true
  } finally {
    ;[busy.value, picking.value] = [false, false]
  }
}
</script>

<template>
  <div data-plan-list>
    <div v-if="starts.length && !forbidden" :class="['flex flex-wrap items-center gap-2', (!actionsTo || picking) && 'mb-3']" data-plan-start>
      <Teleport defer :to="actionsTo || 'body'" :disabled="!actionsTo">
        <UButton
          color="primary"
          :size="actionsTo ? 'md' : 'sm'"
          :disabled="busy"
          :label="busy ? '…' : 'Plan milestone'"
          :aria-expanded="starts.length > 1 ? picking : undefined"
          @click="onPlan"
        />
      </Teleport>
      <!-- The picker: one button per steer project, wrapping on phones. -->
      <div
        v-if="picking && starts.length > 1"
        class="flex basis-full flex-wrap gap-2"
        role="group"
        aria-label="Project of the new plan"
        data-plan-picker
      >
        <UButton
          v-for="p in starts"
          :key="`${p.namespace}/${p.name}`"
          color="neutral"
          variant="outline"
          size="sm"
          class="max-w-full [overflow-wrap:anywhere]"
          :disabled="busy"
          :label="`${p.namespace}/${p.name}`"
          @click="start(p)"
        />
      </div>
    </div>
    <div v-if="startError" class="banner danger" role="alert">{{ startError }}</div>
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
