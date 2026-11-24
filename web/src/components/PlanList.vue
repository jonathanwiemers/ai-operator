<script setup lang="ts">
import { errorMessage } from '../format'
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed, ref, watch } from 'vue'
import UButton from '@nuxt/ui/components/Button.vue'
import PlanRow from './PlanRow.vue'
import { getPlans, LoginRequiredError, NotFoundError } from '../client'
import { planSections } from '../planRows'
import { planNewTo, planStartProjects } from '../planStart'
import type { PlanOverview } from '../generated/views'

// The plan list (decision 12) of every visible project, or of one project:
// Drafting, then Milestones created, each hidden when empty. It loads on
// mount and when the project changes, not live. notFound: no chat, or a
// project the user may not view.
//
// New plan shows when the caller may steer a listed project, also on an
// empty list. It links to the New plan page (/plans/new), with the project
// preselected (?project=) when project is set; the page creates the plan.
// actionsTo: a selector in the host's page header; the button moves there
// (Teleport).
// milestones: the project's milestones where the page has them (the project
// page), so a created milestone shows by title.
const props = defineProps<{ project?: { namespace: string; name: string }; actionsTo?: string; milestones?: { iid: number; title: string }[] }>()
const milestoneTitle = (iid?: number) => (iid ? props.milestones?.find((m) => m.iid === iid)?.title : undefined)
const emit = defineEmits<{ notFound: [] }>()

const overview = ref<PlanOverview | null>(null)
const error = ref('')
const projects = computed(() => overview.value?.projects ?? [])
const sections = computed(() => planSections(overview.value?.plans ?? []))
const canStart = computed(() => planStartProjects(projects.value).length > 0)
const newPlanTo = computed(() => planNewTo(props.project))
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
    // A host without its own not-found page shows an empty list.
    if (e instanceof NotFoundError) {
      overview.value = { plans: [], projects: [] }
      emit('notFound')
    } else error.value = errorMessage(e)
  }
}
// The key, not the object: a host's inline {namespace, name} is new on
// every render.
watch(() => (props.project ? `${props.project.namespace}/${props.project.name}` : ''), load, { immediate: true })
</script>

<template>
  <div data-plan-list>
    <div v-if="canStart" :class="['flex flex-wrap items-center gap-2', !actionsTo && 'mb-3']" data-plan-start>
      <Teleport defer :to="actionsTo || 'body'" :disabled="!actionsTo">
        <UButton color="primary" :size="actionsTo ? 'md' : 'sm'" icon="i-lucide-plus" label="New plan" :to="newPlanTo" data-plan-new />
      </Teleport>
    </div>
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <p v-else-if="!overview" class="muted">Loading…</p>
    <UEmpty v-else-if="!overview.plans.length" icon="i-lucide-map" title="No plans yet." class="section" />
    <template v-else>
      <section v-if="sections.drafting.length" class="section">
        <h2 class="text-[15px]">Drafting</h2>
        <ul class="card m-0 list-none overflow-hidden p-0" aria-label="Drafting">
          <PlanRow v-for="p in sections.drafting" :key="`${p.namespace}/${p.name}`" :item="p" :show-project="!project" :milestone-title="milestoneTitle(p.createdMilestoneIID)" />
        </ul>
      </section>
      <section v-if="sections.created.length" class="section">
        <h2 class="text-[15px]">Milestones created</h2>
        <ul class="card m-0 list-none overflow-hidden p-0" aria-label="Milestones created">
          <PlanRow v-for="p in sections.created" :key="`${p.namespace}/${p.name}`" :item="p" :show-project="!project" :milestone-title="milestoneTitle(p.createdMilestoneIID)" />
        </ul>
      </section>
    </template>
  </div>
</template>
