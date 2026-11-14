<script setup lang="ts">
// Plans list and one plan (R10 entry targets). R11 replaces the plan body
// with chat and the draft editor.
import { ref, watch } from 'vue'
import { getPlan, listPlans, LoginRequiredError, NotFoundError } from '../client'
import { planPhaseTone, planTitle } from '../planRows'
import Badge from '../components/Badge.vue'
import type { PlanListItem, PlanView } from '../generated/views'
import NotFound from './NotFound.vue'

const props = defineProps<{ namespace: string; name: string; plan?: string }>()
const plans = ref<PlanListItem[] | null>(null)
const view = ref<PlanView | null>(null)
const error = ref('')
const notFound = ref(false)

async function load() {
  error.value = ''
  notFound.value = false
  plans.value = null
  view.value = null
  try {
    if (props.plan) view.value = await getPlan(props.namespace, props.name, props.plan)
    else plans.value = await listPlans(props.namespace, props.name)
  } catch (e) {
    if (e instanceof NotFoundError) notFound.value = true
    else if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
  }
}
watch(() => [props.namespace, props.name, props.plan], load, { immediate: true })
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <template v-if="plan">
      <div class="mb-3 flex flex-wrap items-center gap-2">
        <h1 class="m-0">{{ view ? planTitle(view.title, view.createdAt) : plan }}</h1>
        <Badge v-if="view" :tone="planPhaseTone(view.phase)">{{ view.phase }}</Badge>
      </div>
      <div v-if="error" class="banner danger">{{ error }}</div>
      <div v-else-if="!view" class="empty">Loading…</div>
      <pre v-else class="m-0 whitespace-pre-wrap text-[13px] text-(--text-2)">{{ view.draft || 'No draft yet.' }}</pre>
    </template>
    <template v-else>
      <h1 class="mb-3">Plans</h1>
      <div v-if="error" class="banner danger">{{ error }}</div>
      <div v-else-if="!plans" class="empty">Loading…</div>
      <p v-else-if="!plans.length" class="text-(--text-3)">No plans yet.</p>
      <ul v-else class="m-0 grid list-none gap-1 p-0" data-plans>
        <li v-for="p in plans" :key="p.name" class="flex flex-wrap items-center gap-2 px-1.5 py-1">
          <RouterLink :to="{ name: 'plans', params: { namespace, name, plan: p.name } }" class="min-w-0 flex-1 [overflow-wrap:anywhere]">
            {{ planTitle(p.title, p.createdAt) }}
          </RouterLink>
          <Badge :tone="planPhaseTone(p.phase)">{{ p.phase }}</Badge>
        </li>
      </ul>
    </template>
  </div>
</template>
