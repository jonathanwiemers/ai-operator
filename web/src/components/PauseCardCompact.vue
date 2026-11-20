<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import { computed } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { alertColor } from '../badgeTone'
import { pauseEndsBy, pauseTitle, wayOutLine } from '../pauseCard'
import type { ExitView, PauseKind, PromptView } from '../types'
import Truncated from '../ui/Truncated.vue'
import Markdown from './Markdown.vue'
import PromptCard from './PromptCard.vue'

// PauseCardCompact is the pause card's short form for the Inbox, the project
// board and the stream: why the autopilot paused, over which issue, what ends
// it (with exits: the short Way out), and a link to the milestone page,
// where the full card has the actions.
// The reason is untrusted text: inline markdown, clamped to two lines that
// open on tap (Truncated), never a title.
const props = defineProps<{
  kind?: PauseKind
  reason?: string
  issue?: number
  project?: string
  milestoneTitle?: string
  // waiting: the issue a run is working on now (the pause ends by itself).
  waiting?: number
  // exits: the ways out of the pause (milestone views have them).
  exits?: ExitView[]
  // prompt: the open question of this pause (mainRed, budget), shown as its
  // compact card; namespace and projectName say where it is answered.
  prompt?: PromptView
  namespace?: string
  projectName?: string
  to: RouteLocationRaw
  // flat: a row of a list (the Inbox), without the card's border and shadow.
  flat?: boolean
  // hold: the card is the hold of issue ("#N is held"), not the milestone's
  // pause; its Resume (on the full card) lifts only that issue.
  hold?: boolean
}>()

const endsBy = computed(() => pauseEndsBy(props.kind, props.issue || undefined))
const reasonText = computed(() => (props.reason || '').trim())
const way = computed(() => wayOutLine(props.exits))
</script>

<template>
  <!-- The pause's banner (decision 7): a warning UAlert, its Open the action. -->
  <UAlert
    :color="alertColor('warning')"
    variant="subtle"
    orientation="horizontal"
    class="min-w-0 [&_p]:m-0"
    :class="flat ? 'rounded-none px-4 py-3' : 'px-3.5 py-2.5'"
    :ui="{ wrapper: 'gap-1', description: 'flex min-w-0 flex-col gap-1 text-(--text) opacity-100' }"
  >
    <!-- Phones: where it paused wraps below the title; from 760 px both
         share one line. -->
    <template #title>
      <span class="flex min-w-0 flex-wrap items-center gap-x-2.5">
        <strong class="min-w-0 tablet:whitespace-nowrap">{{ pauseTitle(kind, hold ? issue : undefined) }}</strong>
        <span
          v-if="project || milestoneTitle || (issue && !hold)"
          class="muted flex min-w-0 basis-full flex-wrap gap-x-2 text-[13px] font-normal [overflow-wrap:anywhere] tablet:flex-1 tablet:basis-auto"
        >
          <span v-if="project" class="min-w-0 font-semibold">{{ project }}</span>
          <span v-if="milestoneTitle" class="min-w-0 before:me-2 before:content-['·'] first:before:hidden">{{ milestoneTitle }}</span>
          <span v-if="issue && !hold" class="before:me-2 before:content-['·'] first:before:hidden">#{{ issue }}</span>
        </span>
      </span>
    </template>
    <template #description>
      <Truncated v-if="reasonText" class="min-w-0 [overflow-wrap:anywhere] [&_code]:whitespace-normal" :lines="2"><Markdown mode="inline" :text="reasonText" /></Truncated>
      <p v-else-if="!kind" class="muted">Waiting for a human.</p>
      <p v-if="waiting" class="text-[13px] text-(--tone-info-fg)">A run is working on #{{ waiting }} now.</p>
      <Truncated v-if="way" class="faint text-[12.5px]" :text="way" />
      <p v-else-if="endsBy" class="faint text-[12.5px]">{{ endsBy }}</p>
      <PromptCard
        v-if="prompt && namespace && projectName"
        compact
        :namespace="namespace"
        :name="projectName"
        :prompt="prompt"
        :to="to"
      />
    </template>
    <template #actions>
      <UButton :to="to" color="neutral" variant="ghost" size="sm" class="min-h-11 shrink-0 text-[13px] text-(--link) tablet:-my-1 tablet:min-h-0" label="Open" />
    </template>
  </UAlert>
</template>
