<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
// One plan (R11): header with phase, spend / cap, budget raise and (in ⋯) delete;
// chat left, the right pane with the tabs Draft / Decisions, on phones the
// tabs Chat / Decisions / Draft (R6). One ?tab= serves both layouts.
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { deletePlan, getPlan, LoginRequiredError, NotFoundError, raisePlanBudget } from '../client'
import { planPhaseTone, planSpendLabel, planTitle, raiseBudgetError } from '../planRows'
import { planPaneTab, planPaneTabs, planTabs, tabFromQuery, tabQuery, type PlanPaneTab, type PlanTab } from '../streamTabs'
import UButton from '@nuxt/ui/components/Button.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import HeaderActions from '../components/HeaderActions.vue'
import PlanChat from '../components/PlanChat.vue'
import PlanDecisions from '../components/PlanDecisions.vue'
import PlanDecisionWizard from '../components/PlanDecisionWizard.vue'
import PlanDraft from '../components/PlanDraft.vue'
import PlanWizardButton from '../components/PlanWizardButton.vue'
import { toneText } from '../badgeTone'
import { pageHeaderUi } from '../pageHeader'
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
const pane = computed(() => planPaneTab(tab.value))
// paneClass hides a right-pane child: on phones unless it is the tab, on
// tablet and wider unless it is the pane's tab.
const paneClass = (id: PlanPaneTab) => ({ 'max-tablet:hidden': tab.value !== id, 'tablet:hidden': pane.value !== id })
const owner = computed(() => !!view.value?.permissions.steer)
const wizard = ref(false)

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
    await router.push({ name: 'stream-project', params: { namespace: props.namespace, name: props.name }, query: { tab: 'plans' } })
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
    <UPageHeader :title="view ? planTitle(view.title, view.createdAt) : plan" :ui="pageHeaderUi">
      <template v-if="view" #headline>
        <span :class="toneText[planPhaseTone(view.phase)]" data-plan-phase>{{ view.phase }}</span>
      </template>
      <template v-if="view" #description>
        <span data-plan-spend>{{ planSpendLabel(view.spendUSD, view.budgetUSD) }}</span>
        <template v-if="view.createdMilestones.length">
          · {{ view.createdMilestones.length > 1 ? 'Milestones' : 'Milestone' }}
          <template v-for="(m, i) in view.createdMilestones" :key="m.iid">{{ i ? ', ' : '' }}<a :href="m.url">%{{ m.iid }}</a></template>
        </template>
      </template>
      <!-- Raise budget is the one action; Delete is in ⋯. -->
      <template v-if="view && owner" #links>
        <HeaderActions :actions="[{ label: 'Delete', color: 'error', onSelect: onDelete }]" :shown="0">
          <form class="flex items-center gap-1.5" @submit.prevent="onRaise">
            <UInput v-model="raise" class="w-24" size="sm" inputmode="decimal" :placeholder="`≤ ${view.budgetLimitUSD}`" aria-label="New budget in USD" />
            <UButton type="submit" color="neutral" variant="outline" size="sm" label="Raise budget" />
          </form>
        </HeaderActions>
      </template>
    </UPageHeader>
    <UAlert v-if="raiseError" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="raiseError" />
    <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
    <div v-if="!view" class="empty">Loading…</div>
    <template v-else>
      <RouteTabs class="mb-3 w-fit tablet:hidden" :tabs="planTabs" :active="tab" :to="tabTo" label="Plan"
        :badges="{ decisions: view.decisionsPending }" />
      <div class="grid gap-4 tablet:grid-cols-2">
        <PlanChat :class="{ 'max-tablet:hidden': tab !== 'chat' }" :namespace="namespace" :name="name" :plan="plan"
          :can-post="owner" :phase="view.phase" :spend-u-s-d="view.spendUSD" :budget-u-s-d="view.budgetUSD" :decisions="view.decisions ?? []" />
        <div class="flex min-w-0 flex-col" :class="{ 'max-tablet:hidden': tab === 'chat' }">
          <RouteTabs class="mb-3 w-fit max-tablet:hidden" :tabs="planPaneTabs" :active="pane" :to="tabTo" label="Plan pane"
            :badges="{ decisions: view.decisionsPending }" />
          <PlanDraft :class="paneClass('draft')" :namespace="namespace" :name="name" :plan="plan"
            :view="view" :can-edit="owner && view.phase !== 'Created'" @view="(v) => (view = v)" @created="load">
            <template #actions><PlanWizardButton :decisions="view.decisions ?? []" :phase="view.phase" @open="wizard = true" /></template>
          </PlanDraft>
          <PlanDecisions :class="paneClass('decisions')" :namespace="namespace" :name="name" :plan="plan"
            :decisions="view.decisions ?? []" :pending="view.decisionsPending">
            <template v-if="owner" #actions><PlanWizardButton :decisions="view.decisions ?? []" :phase="view.phase" @open="wizard = true" /></template>
          </PlanDecisions>
        </div>
      </div>
      <PlanDecisionWizard v-if="owner" v-model:open="wizard" :namespace="namespace" :name="name" :plan="plan" :view="view"
        @view="(v) => (view = v)" @sent="load" />
    </template>
  </div>
</template>
