<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UCollapsible from '@nuxt/ui/components/Collapsible.vue'
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import USwitch from '@nuxt/ui/components/Switch.vue'
import { ref, watch } from 'vue'
import { alertColor } from '../../badgeTone'
import { clockTime } from '../../format'
import { useNow } from '../../live'
import { searchToolbarUi } from '../../pageHeader'
import type { CockpitGroup, CockpitItem } from '../../stream'
import type { ProjectDetail, PromptView } from '../../types'
import { useCockpitStream, type CockpitStreamScope } from '../../useCockpitStream'
import EventCard from '../stream/EventCard.vue'

// Cockpit is a Cockpit tab (R10 of "Desk: the Cockpit"): the pinned cards
// on top (the pinned slot; a milestone's MilestoneNeeds), a toolbar with
// Expand all and the search, then the stream sorted by cockpitItems, newest
// at the bottom, with Load older above it. Shown events are event cards;
// folded ones are one-line groups (UCollapsible, closed by default) whose
// trigger is a button with the summary and the time span. The composer
// slot comes last (a milestone's CockpitComposer); its sent(id) reloads the
// stream and waits for the pilot's answer to message id (expectAnswer),
// reload() only reloads (after a /pilot note).
const props = defineProps<{
  scope: CockpitStreamScope
  project?: ProjectDetail | null
  prompts?: PromptView[]
  canSteer?: boolean
}>()

const stream = useCockpitStream(() => props.scope)
const { items, older, historyOn, loading, loadingOlder, error } = stream
const now = useNow()

const qDraft = ref('')
function applySearch() {
  stream.search(qDraft.value)
}

// A group's open state: what its trigger set, else Expand all. Switching
// Expand all resets every group to it. The state is kept per event, so it
// stays when Load older or a live event adds events to the group (its
// first or last event changes), and two groups of one issue keep their own.
const expandAll = ref(false)
const opened = ref<Record<string, boolean>>({})
watch(expandAll, () => (opened.value = {}))
const eventKey = (e: { kind: string; id: number }) => `${e.kind}:${e.id}`
function isOpen(g: CockpitGroup): boolean {
  for (const e of g.events) {
    const v = opened.value[eventKey(e)]
    if (v !== undefined) return v
  }
  return expandAll.value
}
function setOpen(g: CockpitGroup, open: boolean) {
  const next = { ...opened.value }
  for (const e of g.events) next[eventKey(e)] = open
  opened.value = next
}

// itemKey: an item's unique key, for v-for and a group's open state. A
// group's own key (milestone and issue) repeats when a shown event splits
// an issue's events, so a group is also named by its first event.
function itemKey(item: CockpitItem): string {
  if (item.type === 'event') return `event:${item.event.kind}:${item.event.id}`
  if (item.type === 'thread') return `thread:${item.key}`
  const first = item.events[0]
  return `group:${item.key}:${first?.kind}:${first?.id}`
}

function span(g: CockpitGroup): string {
  const from = clockTime(g.from, now.value)
  const to = clockTime(g.to, now.value)
  return from === to ? from : `${from} – ${to}`
}
</script>

<template>
  <div class="cockpit" data-cockpit>
    <!-- #pinned: an Inbox proposal without a prompt lands here (inboxRoute). -->
    <section id="pinned" aria-label="Pinned" class="scroll-mt-3" data-cockpit-pinned>
      <slot name="pinned" />
    </section>

    <section aria-label="Stream" data-cockpit-stream>
      <UDashboardToolbar class="mt-3" :ui="searchToolbarUi" data-tab-toolbar>
        <template #left>
          <USwitch v-model="expandAll" label="Expand all" data-cockpit-expand />
        </template>
        <template #right>
          <form role="search" class="w-full tablet:w-45" @submit.prevent="applySearch">
            <UInput
              v-model="qDraft"
              type="search"
              placeholder="Search…"
              aria-label="Search the Cockpit"
              icon="i-lucide-search"
              class="w-full"
              :ui="{ base: 'tablet:text-sm/5' }"
            />
          </form>
        </template>
      </UDashboardToolbar>

      <UAlert v-if="!historyOn" :color="alertColor('info')" variant="subtle" class="mt-3" description="History needs the research database." />
      <UAlert v-if="error" :color="alertColor('danger')" variant="subtle" class="mt-3" :description="error" />
      <div v-if="loading && !items.length" class="empty">Loading…</div>
      <UEmpty v-else-if="!items.length" icon="i-lucide-activity" title="No events yet." variant="naked" size="sm" />
      <template v-else>
        <UButton
          v-if="older"
          color="neutral"
          variant="outline"
          size="sm"
          class="older"
          label="Load older"
          :loading="loadingOlder"
          data-cockpit-older
          @click="stream.loadOlder"
        />
        <ol class="items">
          <li v-for="item in items" :key="itemKey(item)">
            <EventCard v-if="item.type === 'thread'" :notes="item.notes" :project="project" :can-steer="canSteer" @posted="stream.reload" />
            <EventCard
              v-else-if="item.type === 'event'"
              :event="item.event"
              :project="project"
              :prompts="prompts"
              :can-steer="canSteer"
              @posted="stream.reload"
            />
            <UCollapsible v-else :open="isOpen(item)" data-cockpit-group @update:open="setOpen(item, $event)">
              <template #default="{ open }">
                <UButton
                  color="neutral"
                  variant="ghost"
                  block
                  class="group-trigger"
                  :trailing-icon="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
                >
                  <span class="summary">{{ item.summary }}</span>
                  <time class="faint" :datetime="item.from">{{ span(item) }}</time>
                </UButton>
              </template>
              <template #content>
                <ol class="group-events">
                  <li v-for="e in item.events" :key="`${e.kind}:${e.id}`">
                    <EventCard :event="e" :project="project" :prompts="prompts" :can-steer="canSteer" @posted="stream.reload" />
                  </li>
                </ol>
              </template>
            </UCollapsible>
          </li>
        </ol>
      </template>
    </section>

    <slot name="composer" :sent="stream.expectAnswer" :reload="stream.reload" />
  </div>
</template>

<style scoped>
.cockpit {
  display: grid;
  gap: 8px;
  min-width: 0;
}
.items,
.group-events {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.group-events {
  padding: 8px 0 4px 16px;
}
.older {
  justify-self: center;
  margin: 4px auto 12px;
  display: flex;
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
