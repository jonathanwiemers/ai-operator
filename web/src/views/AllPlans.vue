<script setup lang="ts">
// /plans (R12): the plans of every project the user may see (PlanList,
// with the project column and the project picker of Plan milestone).
// PlanList puts Plan milestone into the header's links, the primary action.
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import { ref } from 'vue'
import PlanList from '../components/PlanList.vue'
import { pageHeaderUi } from '../pageHeader'
import NotFound from './NotFound.vue'

const notFound = ref(false)
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <UPageHeader title="Plans" :ui="pageHeaderUi">
      <template #links><div id="plans-actions" class="contents" /></template>
    </UPageHeader>
    <PlanList actions-to="#plans-actions" @not-found="notFound = true" />
  </div>
</template>
