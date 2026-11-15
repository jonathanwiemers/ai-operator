<script setup lang="ts">
// One plan (R11): header with phase, spend / cap, budget raise and delete;
// chat left, draft editor right, on phones tabs Chat / Draft.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { deletePlan, getPlan, LoginRequiredError, NotFoundError, raisePlanBudget } from '../client'
import { planPhaseTone, planSpendLabel, planTitle, raiseBudgetError } from '../planRows'
import { planTabs, tabFromQuery, tabQuery, type PlanTab } from '../streamTabs'
import UButton from '@nuxt/ui/components/Button.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import Badge from '../components/Badge.vue'
import PlanChat from '../components/PlanChat.vue'
import PlanDraft from '../components/PlanDraft.vue'
import RouteTabs from '../ui/RouteTabs.vue'
import type { PlanView } from '../generated/views'
import NotFound from './NotFound.vue'

const props = defineProps<{ namespace: string; name: string; plan: string }>()
const route = useRoute()
const router = useRouter()
const view = ref<PlanView | null>(null)
const [error, notFound, raise, raiseError] = [ref(''), ref(false), ref(''), ref('')]
const tab = computed(() => tabFromQuery(route.query.tab, planTabs))
const tabTo = (id: PlanTab) => ({ query: tabQuery(route.query, id, planTabs) })
const owner = computed(() => !!view.value?.permissions.steer)

async function load() {
  try {
    view.value = await getPlan(props.namespace, props.name, props.plan)
    error.value = ''
  } catch (e) {
    if (e instanceof NotFoundError) notFound.value = true
    else if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
  }
}
async function onRaise() {
  const v = view.value
  if (!v || (raiseError.value = raiseBudgetError(raise.value, v.budgetUSD, v.budgetLimitUSD))) return
  try {
    view.value = await raisePlanBudget(props.namespace, props.name, props.plan, raise.value.trim().replace(/^\$/, ''))
    raise.value = ''
  } catch (e) {
    raiseError.value = e instanceof Error ? e.message : String(e)
  }
}
async function onDelete() {
  if (!view.value || !window.confirm(`Delete the plan ${planTitle(view.value.title, view.value.createdAt)}?`)) return
  try {
    await deletePlan(props.namespace, props.name, props.plan)
    await router.push({ name: 'plans', params: { namespace: props.namespace, name: props.name } })
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

let timer: number | undefined
watch(
  () => [props.namespace, props.name, props.plan],
  () => {
    [view.value, notFound.value] = [null, false]
    void load()
  },
)
onMounted(() => {
  void load()
  timer = window.setInterval(() => {
    if (document.visibilityState !== 'hidden') void load()
  }, 5000)
})
onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <div class="mb-3 flex flex-wrap items-center gap-2">
      <h1 class="m-0">{{ view ? planTitle(view.title, view.createdAt) : plan }}</h1>
      <template v-if="view">
        <Badge :tone="planPhaseTone(view.phase)">{{ view.phase }}</Badge>
        <span class="text-(--text-2)" data-plan-spend>{{ planSpendLabel(view.spendUSD, view.budgetUSD) }}</span>
        <a v-if="view.createdMilestoneIID" :href="view.createdMilestoneURL">Milestone %{{ view.createdMilestoneIID }}</a>
        <form v-if="owner" class="ml-auto flex items-center gap-1.5" @submit.prevent="onRaise">
          <UInput v-model="raise" class="w-24" size="sm" inputmode="decimal" :placeholder="`≤ ${view.budgetLimitUSD}`" aria-label="New budget in USD" />
          <UButton type="submit" color="neutral" variant="outline" size="sm" label="Raise budget" />
          <UButton color="neutral" variant="outline" size="sm" class="text-(--tone-danger-fg)" label="Delete" @click="onDelete" />
        </form>
      </template>
    </div>
    <div v-if="raiseError" class="banner danger">{{ raiseError }}</div>
    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="!view" class="empty">Loading…</div>
    <template v-else>
      <RouteTabs class="mb-3 w-fit tablet:hidden" :tabs="planTabs" :active="tab" :to="tabTo" label="Plan" />
      <div class="grid gap-4 tablet:grid-cols-2">
        <PlanChat :class="{ 'max-tablet:hidden': tab !== 'chat' }" :namespace="namespace" :name="name" :plan="plan"
          :can-post="owner" :phase="view.phase" :spend-u-s-d="view.spendUSD" :budget-u-s-d="view.budgetUSD" />
        <PlanDraft :class="{ 'max-tablet:hidden': tab !== 'draft' }" :namespace="namespace" :name="name" :plan="plan"
          :view="view" :can-edit="owner && view.phase !== 'Created'" @view="(v) => (view = v)" @created="load" />
      </div>
    </template>
  </div>
</template>
