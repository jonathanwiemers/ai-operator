<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import { alertColor } from '../badgeTone'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed, ref } from 'vue'
import UButton from '@nuxt/ui/components/Button.vue'
import Badge from './Badge.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'
import { decisionBadge, decisionState, markedOption, personLabel, whoLine } from '../planDecisions'
import type { PlanDecision } from '../generated/api'
import type { PlanDecisionHistoryView, PlanDecisionVersionView } from '../generated/views'

// One decision of a plan's Decisions tab (R6), in the look of
// DecisionCard.vue (D617-P2): the title, the ID and a state badge, the
// question, the options with the recommended or signed-off one marked, why,
// and the who-line. History loads the plan's history once (loadHistory
// caches it for all cards) and lists this decision's versions with their
// sign-offs, newest first. Agent text is markdown without HTML.
const props = defineProps<{ decision: PlanDecision; loadHistory: () => Promise<PlanDecisionHistoryView> }>()

const badge = computed(() => decisionBadge(decisionState(props.decision)))
const marked = computed(() => markedOption(props.decision))
const [open, loading, error] = [ref(false), ref(false), ref('')]
const versions = ref<PlanDecisionVersionView[] | null>(null)
const unavailable = ref(false)

async function toggle() {
  open.value = !open.value
  if (!open.value || versions.value || loading.value) return
  loading.value = true
  try {
    const h = await props.loadHistory()
    unavailable.value = !h.available
    versions.value = h.decisions.find((d) => d.id === props.decision.id)?.versions ?? []
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <article :id="decision.id" class="card min-w-0 scroll-mt-3 px-3.5 py-3 text-[13px]" data-plan-decision>
    <header class="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
      <h3 class="m-0 min-w-0 text-sm font-semibold tracking-normal text-inherit normal-case [overflow-wrap:anywhere]" data-decision-title>
        <Markdown :text="decision.title || decision.id" mode="inline" />
      </h3>
      <span class="font-medium whitespace-nowrap tabular-nums text-(--text-3)" data-decision-id>{{ decision.id }}</span>
      <Badge class="self-center" :tone="badge.tone" data-decision-state>{{ badge.label }}</Badge>
    </header>
    <Markdown v-if="decision.question" class="mt-2" :text="decision.question" data-decision-question />
    <ul v-if="decision.options?.length" class="m-0 mt-2 flex list-none flex-col gap-1.5 p-0" aria-label="Options" data-options>
      <li
        v-for="(o, n) in decision.options"
        :key="n"
        class="flex min-w-0 items-start gap-2 rounded-(--radius-sm) px-2.5 py-1.5"
        :class="marked?.index === n ? 'bg-(--tone-success-bg)' : 'bg-(--surface-2) text-(--text-2)'"
        :data-marked="marked?.index === n || undefined"
      >
        <Badge v-if="marked?.index === n" :tone="decision.signedOffBy ? 'success' : 'info'" class="flex-none">{{ marked.label }}</Badge>
        <Markdown class="min-w-0 flex-1" :text="o" />
      </li>
    </ul>
    <div v-if="decision.why" class="mt-2" data-decision-why>
      <span class="text-[11.5px] font-semibold tracking-[0.04em] text-(--text-3) uppercase">Why</span>
      <Markdown :text="decision.why" />
    </div>
    <footer class="mt-2.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-(--text-3)">
      <span class="min-w-0 [overflow-wrap:anywhere]" data-who-line>
        {{ whoLine(decision) }} · <RelTime :time="decision.signedOffAt || decision.decidedAt" />
      </span>
      <UButton class="ml-auto min-h-11 tablet:min-h-0" size="xs" color="neutral" variant="ghost"
        :icon="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" :aria-expanded="open" label="History"
        data-history-toggle @click="toggle" />
    </footer>
    <section v-if="open" class="mt-2 border-t border-(--border) pt-2 text-[12.5px]" aria-label="History" data-history>
      <div v-if="loading" class="text-(--text-3)">Loading…</div>
      <UAlert v-else-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
      <div v-else-if="unavailable" class="text-(--text-3)">The history is not available.</div>
      <UEmpty v-else-if="!versions?.length" icon="i-lucide-history" title="No history yet." variant="naked" size="xs" />
      <ol v-else class="m-0 flex list-none flex-col gap-2 p-0">
        <li v-for="v in versions" :key="v.version" class="min-w-0" data-history-version>
          <div class="flex min-w-0 flex-wrap items-baseline gap-x-2">
            <span class="font-semibold tabular-nums">Version {{ v.version }}</span>
            <span class="min-w-0 [overflow-wrap:anywhere] text-(--text-2)">
              Decided by {{ personLabel(v.decidedBy, v.decidedByName) }} · <RelTime :time="v.decidedAt" />
            </span>
          </div>
          <Markdown v-if="v.options[v.recommended]" class="text-(--text-2)" :text="v.options[v.recommended]!" />
          <ul class="m-0 list-none p-0 text-(--text-3)">
            <li v-if="!v.signOffs.length">Not signed off</li>
            <li v-for="s in v.signOffs" :key="`${s.by}-${s.at}`" class="[overflow-wrap:anywhere]" data-history-signoff>
              Signed off by {{ personLabel(s.by, s.byName) }} · <RelTime :time="s.at" />
            </li>
          </ul>
        </li>
      </ol>
    </section>
  </article>
</template>
