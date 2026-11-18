<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import Badge from './Badge.vue'
import DecisionChain from './DecisionChain.vue'
import DecisionRevisit from './DecisionRevisit.vue'
import ExtLink from './ExtLink.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'
import { kindLabel, optionsOf } from '../decisions'
import type { DecisionView } from '../generated/views'

// One decision of the Decisions tab (R12), anchored at #<id>: the title,
// the ID and a Plan or Implementation chip, the question, the options with
// the choice first and marked Chosen, why, the run and when, and the GitLab
// comment. A superseded card is muted and links to its successor's card on
// the same page. Title, question, options and why are agent text: markdown
// without HTML (Markdown.vue). linked highlights the card a #<id> link
// opened. Below come the chain of a revisited decision and the revisit
// block (R13); byID is the loaded list the chain follows successors in.
const props = defineProps<{
  namespace: string
  name: string
  decision: DecisionView
  byID: ReadonlyMap<string, DecisionView>
  linked?: boolean
}>()

const route = useRoute()
const options = computed(() => optionsOf(props.decision))
const successorTo = computed(() => ({ query: route.query, hash: `#${props.decision.supersededBy}` }))
</script>

<template>
  <article
    :id="decision.id"
    class="decision card min-w-0 scroll-mt-3 px-3.5 py-3 text-[13px]"
    :class="{ superseded: decision.superseded, linked }"
    data-decision
  >
    <header class="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
      <h4 class="m-0 min-w-0 text-sm font-semibold [overflow-wrap:anywhere]" data-decision-title>
        <Markdown :text="decision.title || decision.id" mode="inline" />
      </h4>
      <span class="font-medium whitespace-nowrap tabular-nums text-(--text-3)" data-decision-id>{{ decision.id }}</span>
      <Badge class="self-center" :tone="decision.kind === 'planner' ? 'info' : 'neutral'">{{ kindLabel(decision.kind) }}</Badge>
    </header>
    <p v-if="decision.superseded" class="mt-1.5 mb-0 font-medium text-(--text-2)" data-superseded>
      <template v-if="decision.supersededBy">
        Superseded by <RouterLink :to="successorTo" class="tabular-nums">{{ decision.supersededBy }}</RouterLink>
      </template>
      <template v-else>Superseded</template>
    </p>
    <Markdown v-if="decision.question" class="mt-2" :text="decision.question" data-decision-question />
    <ul v-if="options.length" class="m-0 mt-2 flex list-none flex-col gap-1.5 p-0" aria-label="Options" data-options>
      <li
        v-for="(o, n) in options"
        :key="n"
        class="flex min-w-0 items-start gap-2 rounded-(--radius-sm) px-2.5 py-1.5"
        :class="o.chosen ? 'bg-(--tone-success-bg)' : 'bg-(--surface-2) text-(--text-2)'"
        :data-chosen="o.chosen || undefined"
      >
        <Badge v-if="o.chosen" tone="success" class="flex-none">Chosen</Badge>
        <Markdown class="min-w-0 flex-1" :text="o.text" />
      </li>
    </ul>
    <div v-if="decision.why" class="mt-2" data-decision-why>
      <span class="text-[11.5px] font-semibold tracking-[0.04em] text-(--text-3) uppercase">Why</span>
      <Markdown :text="decision.why" />
    </div>
    <footer class="mt-2.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-(--text-3)">
      <span v-if="decision.runID" class="min-w-0 [overflow-wrap:anywhere]">run {{ decision.runID }}</span>
      <RelTime :time="decision.postedAt" />
      <ExtLink v-if="decision.url" :href="decision.url" class="min-h-11 tablet:min-h-0">on GitLab</ExtLink>
    </footer>
    <DecisionChain :namespace="namespace" :name="name" :decision="decision" :byID="byID" />
    <DecisionRevisit :namespace="namespace" :name="name" :decision="decision" />
  </article>
</template>

<style scoped>
.decision {
  transition:
    box-shadow 0.4s,
    background-color 0.4s;
}
.decision.superseded {
  background: var(--surface-2);
  box-shadow: none;
  color: var(--text-2);
}
/* A #<id> link opened this card: a short ring (MilestoneDecisions.vue
   removes the class again). */
.decision.linked {
  box-shadow: 0 0 0 2px var(--focus);
  background: var(--tone-progress-bg);
}
@media (prefers-reduced-motion: reduce) {
  .decision {
    transition: none;
  }
}
</style>
