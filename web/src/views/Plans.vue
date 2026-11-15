<script setup lang="ts">
// Plans list; with :plan the route shows one plan (Plan.vue).
import { ref, watch } from 'vue'
import { listPlans, LoginRequiredError, NotFoundError } from '../client'
import { planPhaseTone, planTitle } from '../planRows'
import Badge from '../components/Badge.vue'
import type { PlanListItem } from '../generated/views'
import NotFound from './NotFound.vue'
import Plan from './Plan.vue'

const props = defineProps<{ namespace: string; name: string; plan?: string }>()
const plans = ref<PlanListItem[] | null>(null)
const error = ref('')
const notFound = ref(false)

async function load() {
  error.value = ''
  notFound.value = false
  plans.value = null
  try {
    plans.value = await listPlans(props.namespace, props.name)
  } catch (e) {
    if (e instanceof NotFoundError) notFound.value = true
    else if (!(e instanceof LoginRequiredError)) error.value = e instanceof Error ? e.message : String(e)
  }
}
watch(
  () => [props.namespace, props.name, props.plan],
  () => {
    if (!props.plan) void load()
  },
  { immediate: true },
)
</script>

<template>
  <Plan v-if="plan" :namespace="namespace" :name="name" :plan="plan" />
  <NotFound v-else-if="notFound" />
  <div v-else class="page">
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
  </div>
</template>
