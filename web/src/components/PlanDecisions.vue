<script setup lang="ts">
import { watch } from 'vue'
import { getPlanDecisionHistory } from '../client'
import PlanDecisionCard from './PlanDecisionCard.vue'
import type { PlanDecision } from '../generated/api'
import type { PlanDecisionHistoryView } from '../generated/views'

// A plan's Decisions tab (R6): a header with the count of open or not
// signed-off decisions (the wizard's button goes into its actions slot) and
// one card per current decision. The history is loaded once, on the first
// History toggle, and shared by all cards; a failed load is tried again,
// and a new version or sign-off drops it (its card is made anew).
const props = defineProps<{ namespace: string; name: string; plan: string; decisions: PlanDecision[]; pending: number }>()

let history: Promise<PlanDecisionHistoryView> | null = null
function loadHistory(): Promise<PlanDecisionHistoryView> {
  history ??= getPlanDecisionHistory(props.namespace, props.name, props.plan).catch((e) => {
    history = null
    throw e
  })
  return history
}
const cardKey = (d: PlanDecision) => `${d.id}-${d.version}-${d.signedOffAt ?? ''}`
watch(() => props.decisions.map(cardKey).join(' '), () => (history = null))
</script>

<template>
  <section class="flex min-w-0 flex-col gap-2" aria-label="Decisions" data-plan-decisions>
    <header class="flex min-h-11 flex-wrap items-center gap-2">
      <h2 class="m-0 text-base">Decisions</h2>
      <span v-if="pending" class="text-(--text-2)" data-decisions-pending>{{ pending }} open or not signed off</span>
      <div class="ml-auto flex items-center gap-1.5"><slot name="actions" /></div>
    </header>
    <div v-if="!decisions.length" class="empty" data-decisions-empty>No decisions yet.</div>
    <PlanDecisionCard v-for="d in decisions" :key="cardKey(d)" :decision="d" :load-history="loadHistory" />
  </section>
</template>
