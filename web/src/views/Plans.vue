<script setup lang="ts">
// Plans list of the project (PlanList); with :plan the route shows one plan
// (Plan.vue).
import { ref, watch } from 'vue'
import PlanList from '../components/PlanList.vue'
import NotFound from './NotFound.vue'
import Plan from './Plan.vue'

const props = defineProps<{ namespace: string; name: string; plan?: string }>()
const notFound = ref(false)
watch(
  () => [props.namespace, props.name],
  () => (notFound.value = false),
)
</script>

<template>
  <Plan v-if="plan" :namespace="namespace" :name="name" :plan="plan" />
  <NotFound v-else-if="notFound" />
  <div v-else class="page">
    <h1 class="mb-3">Plans</h1>
    <PlanList :project="{ namespace, name }" @not-found="notFound = true" />
  </div>
</template>
