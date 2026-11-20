<script setup lang="ts">
import { computed } from 'vue'
import { hasFeature } from '../api'
import { cost } from '../format'
import { pilotStreamKinds, slashSuggestions } from '../stream'
import { useStreamNote } from '../streamNote'
import type { MilestoneView, ProjectDetail } from '../types'
import AskPilot from './AskPilot.vue'
import Badge from './Badge.vue'
import Composer from './Composer.vue'
import IssueStream from './IssueStream.vue'

// MilestonePilot is a milestone's Pilot tab: the pilot line (autonomy,
// cost, Pilot task), the composer to the pilot and the milestone's pilot
// events (fixed kinds, no chips). The pause cards, the prompts, the older
// proposals and the pause history are on the Needs you tab (R12). A stored
// milestone has no pilot: only its events show.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView; project: ProjectDetail | null }>()

const active = computed(() => !props.milestone.stored)
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

// Utilities only (docs/contributing/web-ui.md, "Milestone Pilot"). On phones the
// Pilot task link is 44 px high; the composer and the chat bring their own
// touch sizes.
const sectionHead =
  'h3 m-0 mb-2 flex justify-between gap-2 text-[11.5px] font-semibold tracking-[0.06em] text-(--text-3) uppercase'
const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
const quiet = 'text-[13px] text-(--text-3)'
</script>

<template>
  <div class="grid max-w-[860px] min-w-0 gap-[18px]">
    <template v-if="active">
      <div v-if="milestone.pilot" class="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px]" data-pilot>
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

      <section class="min-w-0">
        <h2 :class="sectionHead"><span>Message the pilot</span></h2>
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
      <h2 :class="sectionHead"><span>Pilot events</span></h2>
      <IssueStream :namespace="namespace" :name="name" :milestone="milestone.iid" :kinds="pilotStreamKinds" :prompts="milestone.prompts" :can-steer="active && !!perms?.steer" />
    </section>
  </div>
</template>
