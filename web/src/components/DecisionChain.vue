<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import Badge from './Badge.vue'
import ExtLink from './ExtLink.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'
import { chainOf, followUpLabel, issueStateLabel, type ChainStep } from '../decisions'
import type { DecisionView } from '../generated/views'

// DecisionChain is a revisited decision's chain (R13), one step per
// request, oldest first, through its successors (chainOf): who asked, the
// instruction (agent or human text: markdown without HTML), where (the
// comment on GitLab, or the follow-up issue's Desk page with its state),
// and, once applied, the decision that replaced it, linked to its card.
const props = defineProps<{ namespace: string; name: string; decision: DecisionView; byID: ReadonlyMap<string, DecisionView> }>()

const route = useRoute()
const steps = computed(() => chainOf(props.decision, props.byID))

function followUpTo(s: ChainStep) {
  const { namespace, name, decision } = props
  const iid = String(decision.milestoneIID)
  return { name: 'issue' as const, params: { namespace, name, iid, issue: String(s.request.followUpIID) } }
}
const cardTo = (id: string) => ({ query: route.query, hash: `#${id}` })

const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
</script>

<template>
  <ol v-if="steps.length" class="m-0 mt-3 flex list-none flex-col gap-2 border-l-2 border-(--border) p-0 pl-3" aria-label="Revisits" data-chain>
    <li v-for="(s, n) in steps" :key="n" class="min-w-0" data-chain-step>
      <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[12.5px] text-(--text-3)">
        <span class="font-medium text-(--text-2)" data-requested-by>{{ s.request.requestedBy || 'Someone' }}</span>
        <span>asked to revisit <span class="tabular-nums">{{ s.decision }}</span></span>
        <template v-if="followUpLabel(s.request)">
          <RouterLink :to="followUpTo(s)" class="tabular-nums" :class="tap" data-follow-up>{{ followUpLabel(s.request) }}</RouterLink>
          <Badge v-if="s.request.followUpState" :tone="s.request.followUpState === 'opened' ? 'progress' : 'muted'" data-follow-up-state>
            {{ issueStateLabel(s.request.followUpState) }}
          </Badge>
        </template>
        <ExtLink v-else-if="s.request.url" :href="s.request.url" :class="tap" data-comment>comment</ExtLink>
        <RelTime :time="s.request.at" />
      </div>
      <Markdown v-if="s.request.instruction" class="mt-0.5 [overflow-wrap:anywhere]" :text="s.request.instruction" data-instruction />
      <p v-if="s.successor" class="m-0 mt-0.5 text-[12.5px]" data-replaced-by>
        Replaced by
        <RouterLink :to="cardTo(s.successor)" class="tabular-nums" :class="tap">{{ s.successor }}</RouterLink>
        <template v-if="s.successorTitle">
          · <Markdown mode="inline" :text="s.successorTitle" />
        </template>
      </p>
    </li>
  </ol>
</template>
