<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { hasFeature } from '../api'
import { cost } from '../format'
import { pausePrompt } from '../pauseCard'
import { promptAnchor, splitPrompts } from '../promptCard'
import { pilotStreamKinds } from '../stream'
import type { MilestoneView, ProjectDetail } from '../types'
import AskPilot from './AskPilot.vue'
import Badge from './Badge.vue'
import IssueStream from './IssueStream.vue'
import PauseCard from './PauseCard.vue'
import PausesCard from './PausesCard.vue'
import PromptCard from './PromptCard.vue'
import StreamComposer from './StreamComposer.vue'

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
</script>

<template>
  <div class="pilot-tab">
    <template v-if="active">
      <PauseCard v-if="paused" :key="milestone.iid" :namespace="namespace" :name="name" :milestone="milestone" />

      <section v-if="milestone.pilot || prompts.open.length || prompts.closed.length" class="panel">
        <h3>
          <span>Questions</span>
          <span v-if="prompts.open.length">{{ prompts.open.length }} open</span>
        </h3>
        <div v-if="milestone.pilot" class="row pilot">
          <span class="faint">Pilot</span>
          <Badge :tone="milestone.pilot.autonomy === 'managed' ? 'progress' : 'neutral'">{{ milestone.pilot.autonomy }}</Badge>
          <span class="faint">{{ cost(milestone.pilot.costUSD) }}</span>
          <span class="spacer" />
          <RouterLink :to="{ name: 'task', params: { namespace, name, task: milestone.pilot.task } }">Pilot task</RouterLink>
        </div>
        <div class="prompts">
          <div v-if="!prompts.open.length" class="empty-sm">No open questions.</div>
          <PromptCard v-for="p in prompts.open" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
          <details v-if="prompts.closed.length" class="earlier" :open="closedLinked">
            <summary>Earlier questions ({{ prompts.closed.length }})</summary>
            <div class="prompts">
              <PromptCard v-for="p in prompts.closed" :key="p.id" :namespace="namespace" :name="name" :prompt="p" />
            </div>
          </details>
        </div>
      </section>

      <section class="panel">
        <h3><span>Message the pilot</span></h3>
        <AskPilot v-if="showChat" :namespace="namespace" :name="name" :milestone="milestone" />
        <StreamComposer
          v-else-if="perms?.steer"
          :namespace="namespace"
          :name="name"
          :project="project"
          :milestone-iid="milestone.iid"
          steer
        />
        <p v-if="!perms?.chat && !perms?.steer" class="empty-sm">Read-only: only the milestone's owners can message the pilot.</p>
      </section>
    </template>

    <section class="panel">
      <h3><span>Pilot events</span></h3>
      <IssueStream :namespace="namespace" :name="name" :milestone="milestone.iid" :kinds="pilotStreamKinds" :can-steer="active && !!perms?.steer" />
    </section>

    <PausesCard v-if="milestone.pauses" :pauses="milestone.pauses" />
  </div>
</template>

<style scoped>
.pilot-tab {
  display: grid;
  gap: 18px;
  max-width: 860px;
}
.panel h3 {
  font-size: 11.5px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-3);
  margin: 0 0 8px;
  display: flex;
  justify-content: space-between;
  font-weight: 600;
}
.pilot {
  font-size: 13px;
  margin-bottom: 10px;
}
.prompts {
  display: grid;
  gap: 10px;
}
.earlier summary {
  cursor: pointer;
  font-size: 13px;
  color: var(--text-2);
}
.earlier[open] summary {
  margin-bottom: 10px;
}
.empty-sm {
  font-size: 13px;
  color: var(--text-3);
}
</style>
