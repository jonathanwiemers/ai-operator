<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { hasFeature } from '../api'
import { cost } from '../format'
import { pausePrompt } from '../pauseCard'
import { promptAnchor, splitPrompts } from '../promptCard'
import { pilotStreamKinds, slashSuggestions } from '../stream'
import { useStreamNote } from '../streamNote'
import type { MilestoneView, ProjectDetail } from '../types'
import AskPilot from './AskPilot.vue'
import Badge from './Badge.vue'
import Composer from './Composer.vue'
import IssueStream from './IssueStream.vue'
import PauseCard from './PauseCard.vue'
import PausesCard from './PausesCard.vue'
import PromptCard from './PromptCard.vue'

// MilestonePilot is a milestone's Pilot tab (R11): the full pause card while
// paused, the prompts (open first, the closed ones folded), the composer to
// the pilot, the milestone's pilot events (fixed kinds, no chips) and the
// pause history. A stored milestone has no pilot: only its history shows.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView; project: ProjectDetail | null }>()

const active = computed(() => !props.milestone.stored)
const paused = computed(() => active.value && props.milestone.autopilot?.phase === 'Paused')
// The pause card shows its own prompt (mainRed, budget): the list leaves it
// out so one prompt is not two answer widgets with the same anchor.
const prompts = computed(() => {
  const s = splitPrompts(props.milestone.prompts)
  const own = paused.value ? pausePrompt(props.milestone.autopilot?.pauseKind, props.milestone.prompts) : undefined
  return own ? { open: s.open.filter((p) => p.id !== own.id), closed: s.closed } : s
})
// A Desk link to a closed prompt unfolds the closed ones.
const route = useRoute()
const closedLinked = computed(() => prompts.value.closed.some((p) => route.hash === `#${promptAnchor(p.id)}`))
// The composer: the Desk chat with the chat grant, else a /pilot comment
// on the tracking issue with steer, else read-only.
const perms = computed(() => props.milestone.permissions)
const showChat = computed(() => !!perms.value?.chat || (hasFeature('chat') && !perms.value?.steer))
const note = useStreamNote(() => ({
  namespace: props.namespace,
  name: props.name,
  project: props.project,
  milestoneIID: props.milestone.iid,
  steer: true,
  shown: active.value && !showChat.value && !!perms.value?.steer,
}))

// Utilities only (docs/ui.md, "Milestone Pilot"). On phones the links and
// the Earlier questions fold are 44 px high; the prompt cards, the composer
// and the chat bring their own touch sizes.
const sectionHead =
  'm-0 mb-2 flex justify-between gap-2 text-[11.5px] font-semibold tracking-[0.06em] text-(--text-3) uppercase'
const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
const quiet = 'text-[13px] text-(--text-3)'
</script>

<template>
  <div class="grid max-w-[860px] min-w-0 gap-[18px]">
    <template v-if="active">
      <PauseCard v-if="paused" :key="milestone.iid" :namespace="namespace" :name="name" :milestone="milestone" />

      <section v-if="milestone.pilot || prompts.open.length || prompts.closed.length" class="min-w-0">
        <h3 :class="sectionHead">
          <span>Questions</span>
          <span v-if="prompts.open.length">{{ prompts.open.length }} open</span>
        </h3>
        <div v-if="milestone.pilot" class="mb-2.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px]" data-pilot>
          <span class="faint">Pilot</span>
          <Badge :tone="milestone.pilot.autonomy === 'managed' ? 'progress' : 'neutral'">{{ milestone.pilot.autonomy }}</Badge>
          <span class="faint tabular-nums">{{ cost(milestone.pilot.costUSD) }}</span>
          <RouterLink
            class="ml-auto"
            :class="tap"
            :to="{ name: 'task', params: { namespace, name, task: milestone.pilot.task } }"
          >
            Pilot task
          </RouterLink>
        </div>
        <div class="grid gap-2.5">
          <div v-if="!prompts.open.length" :class="quiet">No open questions.</div>
          <PromptCard v-for="p in prompts.open" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
          <details v-if="prompts.closed.length" class="group" :open="closedLinked" data-earlier>
            <summary class="min-h-11 cursor-pointer py-3 text-[13px] text-(--text-2) tablet:min-h-0 tablet:py-0 group-open:mb-2.5">
              Earlier questions ({{ prompts.closed.length }})
            </summary>
            <div class="grid gap-2.5">
              <PromptCard v-for="p in prompts.closed" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
            </div>
          </details>
        </div>
      </section>

      <section class="min-w-0">
        <h3 :class="sectionHead"><span>Message the pilot</span></h3>
        <AskPilot v-if="showChat" :namespace="namespace" :name="name" :milestone="milestone" />
        <Composer
          v-else-if="perms?.steer"
          v-model="note.body"
          v-model:target="note.target"
          mode="note"
          :placeholder="note.placeholder"
          :hint="note.hint"
          :busy="note.sending"
          :disabled="!!note.disabledReason"
          :error="note.error"
          :targets="note.targets"
          :suggestions="slashSuggestions"
          :blocked="note.blocked"
          :connect-url="note.connectUrl"
          @send="note.send"
        />
        <p v-if="!perms?.chat && !perms?.steer" :class="quiet">Read-only: only the milestone's owners can message the pilot.</p>
      </section>
    </template>

    <section class="min-w-0">
      <h3 :class="sectionHead"><span>Pilot events</span></h3>
      <IssueStream :namespace="namespace" :name="name" :milestone="milestone.iid" :kinds="pilotStreamKinds" :can-steer="active && !!perms?.steer" />
    </section>

    <PausesCard v-if="milestone.pauses" :pauses="milestone.pauses" />
  </div>
</template>
