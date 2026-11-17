<script setup lang="ts">
// /plans (R12): the plans of every project the user may see (PlanList,
// with the project column and the project picker of Plan milestone).
import { ref } from 'vue'
import PlanList from '../components/PlanList.vue'
import NotFound from './NotFound.vue'

const notFound = ref(false)
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <h1 class="mb-3">Plans</h1>
    <PlanList @not-found="notFound = true" />
  </div>
</template>
