<script setup lang="ts">
import { computed } from 'vue'
import { answersSummary, changedLines } from '../planDecisions'
import type { PlanDecision } from '../generated/api'
import type { DecisionAnswer } from '../generated/store'
import Markdown from './Markdown.vue'

// ChatAnswers is a plan chat message that carries decision answers: the count
// line, the changed answers and the owner's extra text.
const props = defineProps<{ answers: DecisionAnswer[]; decisions: PlanDecision[]; body?: string }>()
const summary = computed(() => answersSummary(props.answers))
const lines = computed(() => changedLines(props.answers, props.decisions))
</script>

<template>
  <div data-chat-answers>
    <p class="m-0 text-sm font-semibold">{{ summary.label }}</p>
    <ul v-if="lines.length" class="m-0 mt-1 list-none p-0 text-[13px]">
      <li v-for="l in lines" :key="l.id" class="truncate" :title="l.full">{{ l.text }}</li>
    </ul>
    <Markdown v-if="body" class="mt-2" :text="body" />
  </div>
</template>
