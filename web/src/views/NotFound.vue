<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UPageHeader from '@nuxt/ui/components/PageHeader.vue'
import { pageHeaderUi } from '../pageHeader'
</script>

<template>
  <div class="page">
    <UPageHeader title="Not found" :ui="pageHeaderUi" />
    <div class="rounded-(--radius) border border-default bg-default p-8 text-center text-dimmed shadow-(--shadow)">
      <p class="mb-3">This page does not exist, or you have no access to it.</p>
      <UButton :to="{ name: 'overview' }" icon="i-lucide-house" color="neutral" variant="outline" label="Back to Overview" />
    </div>
  </div>
</template>
