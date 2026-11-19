<script setup lang="ts">
// The wizard's button (R7): Answer N questions / Review decisions, in the
// Decisions tab header and the draft toolbar. The page shows it to owners;
// it hides without decisions and is disabled with a hint while a turn runs
// or after the milestone was created.
import { computed } from 'vue'
import UButton from '@nuxt/ui/components/Button.vue'
import { wizardBlocked, wizardButton } from '../planWizard'
import type { AgentPlanPhase, PlanDecision } from '../generated/api'

const props = defineProps<{ decisions: PlanDecision[]; phase?: AgentPlanPhase }>()
const emit = defineEmits<{ open: [] }>()
const button = computed(() => wizardButton(props.decisions))
const hint = computed(() => wizardBlocked(props.phase))
</script>

<template>
  <span v-if="button.show" class="inline-flex flex-wrap items-center gap-1.5">
    <span v-if="hint" class="text-(--text-2)" data-wizard-hint>{{ hint }}</span>
    <UButton class="min-h-11 tablet:min-h-0" size="sm" :label="button.label" :disabled="!!hint" data-wizard-open @click="emit('open')" />
  </span>
</template>
