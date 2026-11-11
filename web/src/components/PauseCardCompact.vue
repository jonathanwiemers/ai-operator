<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
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
}>()

const endsBy = computed(() => pauseEndsBy(props.kind, props.issue || undefined))
const reasonText = computed(() => (props.reason || '').trim())
const way = computed(() => wayOutLine(props.exits))
</script>

<template>
  <section
    class="flex min-w-0 flex-col gap-1 border-l-3 border-l-(--tone-warning-fg) [&_p]:m-0"
    :class="
      flat
        ? 'py-3 pr-4 pl-[13px]'
        : 'rounded-(--radius) border-y border-r border-(--border) bg-(--surface) px-3.5 py-2.5 shadow-(--shadow)'
    "
  >
    <!-- Phones: the title and Open share the first line, where it paused
         wraps below; from 760 px all three share one line. -->
    <div class="flex min-w-0 flex-wrap items-center gap-x-2.5">
      <strong class="whitespace-nowrap">{{ pauseTitle(kind) }}</strong>
      <span
        v-if="project || milestoneTitle || issue"
        class="muted order-last flex min-w-0 basis-full flex-wrap gap-x-2 text-[13px] break-words tablet:order-none tablet:flex-1 tablet:basis-auto"
      >
        <span v-if="project" class="min-w-0 font-semibold">{{ project }}</span>
        <span v-if="milestoneTitle" class="min-w-0 before:me-2 before:content-['·'] first:before:hidden">{{ milestoneTitle }}</span>
        <span v-if="issue" class="before:me-2 before:content-['·'] first:before:hidden">#{{ issue }}</span>
      </span>
      <UButton :to="to" color="neutral" variant="ghost" size="sm" class="ms-auto min-h-11 shrink-0 text-[13px] text-(--link) tablet:-my-1 tablet:min-h-0" label="Open" />
    </div>
    <Truncated v-if="reasonText" :lines="2"><Markdown mode="inline" :text="reasonText" /></Truncated>
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
  </section>
</template>
