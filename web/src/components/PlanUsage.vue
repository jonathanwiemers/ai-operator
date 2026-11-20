<script setup lang="ts">
import UEmpty from '@nuxt/ui/components/Empty.vue'
// Plan usage (docs/contributing/web-ui.md, "Metrics page"): per engine the freshest usage
// reading among the viewer's projects, with the project it came from and its
// age. Live state, not bound to the range or the filters. No chart.
import { RouterLink } from 'vue-router'
import type { PlanUsageView } from '../generated/views'
import EngineUsage from './EngineUsage.vue'
import RelTime from './RelTime.vue'

defineProps<{ usage: PlanUsageView[] }>()
</script>

<template>
  <section class="section" aria-labelledby="costs-plan-usage" data-costs-plan>
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="costs-plan-usage">Plan usage</h2>
      <span class="faint">Live state, not bound to the range</span>
    </div>
    <UEmpty v-if="!usage.length" icon="i-lucide-receipt" title="No engine has reported its usage yet." size="sm" data-plan-empty />
    <div v-else class="grid grid-cols-1 gap-3 tablet:grid-cols-2">
      <div v-for="u in usage" :key="u.usage.engine" class="card flex min-w-0 flex-col gap-1 p-3" :data-plan="u.usage.engine">
        <EngineUsage :usage="u.usage" />
        <div class="faint text-[13px] [overflow-wrap:anywhere]">
          from
          <RouterLink :to="`/projects/${u.namespace}/${u.project}`" class="underline">{{ u.namespace }}/{{ u.project }}</RouterLink>,
          <RelTime :time="u.usage.observedAt" />
        </div>
      </div>
    </div>
  </section>
</template>
