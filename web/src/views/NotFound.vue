<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
</script>

<template>
  <div class="mx-auto max-w-[1400px] px-6 pt-6 pb-16">
    <div class="rounded-(--radius) border border-default bg-default p-8 text-center text-dimmed shadow-(--shadow)">
      <h2>Not found</h2>
      <p class="my-3">This page does not exist, or you have no access to it.</p>
      <UButton :to="{ name: 'overview' }" icon="i-lucide-house" color="neutral" variant="outline" label="Back to Overview" />
    </div>
  </div>
</template>
