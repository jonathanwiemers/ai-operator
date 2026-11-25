<script setup lang="ts">
import { computed, inject, ref } from 'vue'
import { useRouter } from 'vue-router'
import { hasFeature } from '../../api'
import { cockpitDecisionsKey, decisionItem } from '../../cockpitDecision'
import type { DecisionView } from '../../generated/views'
import { decisionHref } from '../../milestoneQueue'
import type { StreamEvent } from '../../types'
import DecisionRevisit from '../DecisionRevisit.vue'
import Markdown from '../Markdown.vue'

// CockpitDecision is the body of a decision.recorded event (R5 of "Desk:
// the Cockpit newest first by issue"): the ID, linked to its card on the
// milestone's Decisions tab (decisionHref), the title, the choice, and
// "Superseded by D…". With the stored decision loaded by the Cockpit
// (cockpitDecisionsKey) it also renders that decision's DecisionRevisit, so
// Revisit works in place as on the tab; without it, the event's data only.
const props = defineProps<{ event: StreamEvent }>()

const loaded = inject(cockpitDecisionsKey, ref<ReadonlyMap<string, DecisionView>>(new Map()))
const item = computed(() => decisionItem(props.event.data, loaded.value))

const router = useRouter()
const href = computed(() => {
  const { namespace, project: name, milestoneIID } = props.event
  if (!milestoneIID) return ''
  const path = router.resolve({ name: 'stream-milestone', params: { namespace, name, iid: String(milestoneIID) } }).path
  return decisionHref({ id: item.value.id }, path, hasFeature('history'))
})
</script>

<template>
  <div class="decision" data-cockpit-decision>
    <p class="head">
      <RouterLink v-if="href" :to="href" class="id" data-cockpit-decision-id>{{ item.id }}</RouterLink>
      <span v-else class="id">{{ item.id }}</span>
      <Markdown v-if="item.title" class="title" :text="item.title" mode="inline" />
    </p>
    <p v-if="item.choice" class="choice" data-cockpit-decision-choice>
      <span class="label">Chosen</span> <Markdown :text="item.choice" mode="inline" />
    </p>
    <p v-if="item.superseded" class="superseded" data-superseded>
      <template v-if="item.supersededBy">Superseded by {{ item.supersededBy }}</template>
      <template v-else>Superseded</template>
    </p>
    <DecisionRevisit v-if="item.view" :namespace="event.namespace" :name="event.project" :decision="item.view" />
  </div>
</template>

<style scoped>
.decision {
  display: grid;
  gap: 4px;
  margin-top: 4px;
  font-size: 13px;
}
.decision p {
  margin: 0;
}
.id {
  margin-right: 6px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.label {
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--text-3);
}
.superseded {
  font-weight: 500;
  color: var(--text-2);
}
</style>
