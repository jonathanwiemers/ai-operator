<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UCollapsible from '@nuxt/ui/components/Collapsible.vue'
import { clockTime } from '../../format'
import { useNow } from '../../live'
import type { CockpitGroup, CockpitItem } from '../../stream'
import type { ProjectDetail, PromptView } from '../../types'
import EventCard from '../stream/EventCard.vue'

// CockpitItem is one item of a Cockpit thread: a shown event's card, a note
// thread, or a one-line group of folded events (UCollapsible, its trigger a
// button with the summary and the time span). A group's open state is the
// thread's (open, update:open).
defineProps<{
  item: CockpitItem
  open: boolean
  project?: ProjectDetail | null
  prompts?: PromptView[]
  canSteer?: boolean
}>()
const emit = defineEmits<{ 'update:open': [open: boolean]; posted: [] }>()

const now = useNow()
function span(g: CockpitGroup): string {
  const from = clockTime(g.from, now.value)
  const to = clockTime(g.to, now.value)
  return from === to ? from : `${from} – ${to}`
}
</script>

<template>
  <EventCard v-if="item.type === 'thread'" :notes="item.notes" :project="project" :can-steer="canSteer" @posted="emit('posted')" />
  <EventCard
    v-else-if="item.type === 'event'"
    :event="item.event"
    :project="project"
    :prompts="prompts"
    :can-steer="canSteer"
    @posted="emit('posted')"
  />
  <UCollapsible v-else :open="open" data-cockpit-group @update:open="emit('update:open', $event)">
    <template #default="{ open: shown }">
      <UButton
        color="neutral"
        variant="ghost"
        block
        class="group-trigger"
        :trailing-icon="shown ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
      >
        <span class="summary">{{ item.summary }}</span>
        <time class="faint" :datetime="item.from">{{ span(item) }}</time>
      </UButton>
    </template>
    <template #content>
      <ol class="group-events">
        <li v-for="e in item.events" :key="`${e.kind}:${e.id}`">
          <EventCard :event="e" :project="project" :prompts="prompts" :can-steer="canSteer" @posted="emit('posted')" />
        </li>
      </ol>
    </template>
  </UCollapsible>
</template>

<style scoped>
.group-events {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 8px 0 4px 16px;
  list-style: none;
}
.group-trigger {
  justify-content: space-between;
  gap: 12px;
  font-size: 13.5px;
  text-align: left;
}
.summary {
  min-width: 0;
  overflow-wrap: anywhere;
}
.group-trigger time {
  flex-shrink: 0;
  margin-left: auto;
  font-size: 12px;
}
</style>
