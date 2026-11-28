<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UCollapsible from '@nuxt/ui/components/Collapsible.vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { eventPlace, type ThreadHeader } from '../../cockpitThread'
import type { CockpitGroup, CockpitItem as Item, CockpitThread } from '../../stream'
import type { ProjectDetail, PromptView } from '../../types'
import Badge from '../Badge.vue'
import LiveTranscript from '../LiveTranscript.vue'
import CockpitItem from './CockpitItem.vue'

// CockpitThread is one thread of the Cockpit (R4 of "Desk: the Cockpit
// newest first by issue"): its header (threadHeader: ref, title, state,
// the running step, the milestone on the project Cockpit), with the
// transcript grant a running issue's Live transcript (closed), then its
// newest items and Show N more for the rest (D818-P3). Expand all opens the
// rest and every group. An issue's thread is the target of #issue-<iid>:
// the link opens it fully and scrolls to it, with ?live=1 also its Live
// transcript.
const props = defineProps<{
  thread: CockpitThread
  header: ThreadHeader
  expandAll: boolean
  transcript?: boolean
  project?: ProjectDetail | null
  prompts?: PromptView[]
  canSteer?: boolean
}>()
const emit = defineEmits<{ posted: [] }>()

// newest: the items shown before Show N more.
const newest = 3
const shown = computed(() => props.thread.items.slice(0, newest))
const rest = computed(() => props.thread.items.slice(newest))

// A group's open state: what its trigger set, else Expand all. Switching
// Expand all resets every group to it. The state is kept per event, so it
// stays when Load older or a live event adds events to the group (its
// first or last event changes), and two groups of one issue keep their own.
const opened = ref<Record<string, boolean>>({})
const more = ref(props.expandAll)
watch(
  () => props.expandAll,
  (all) => {
    opened.value = {}
    more.value = all
  },
)
const eventKey = (e: { kind: string; id: number }) => `${e.kind}:${e.id}`
function isOpen(item: Item): boolean {
  if (item.type !== 'group') return false
  for (const e of item.events) {
    const v = opened.value[eventKey(e)]
    if (v !== undefined) return v
  }
  return props.expandAll
}
function setOpen(g: CockpitGroup, open: boolean) {
  const next = { ...opened.value }
  for (const e of g.events) next[eventKey(e)] = open
  opened.value = next
}

// itemKey: an item's unique key, for v-for. A group's own key (milestone
// and issue) repeats when a shown event splits an issue's events, so a
// group is also named by its first event.
function itemKey(item: Item): string {
  if (item.type === 'event') return `event:${item.event.kind}:${item.event.id}`
  if (item.type === 'thread') return `thread:${item.key}`
  const first = item.events[0]
  return `group:${item.key}:${first?.kind}:${first?.id}`
}

const live = computed(() => (props.transcript ? props.header.live : undefined))
const liveOpen = ref(false)

// #issue-<iid> (D818-P1: ?live=1 beside it opens the Live transcript once
// the grant and the run are known): scroll once per link, and once more
// when the page's issue data (title or state) arrives, as the pinned cards
// above may have grown with it.
const route = useRoute()
const el = ref<HTMLElement | null>(null)
let followed = ''
watch(
  () => [route.hash, route.query.live, !!live.value, !!(props.header.title || props.header.state)] as const,
  ([hash, wantLive, hasLive, loaded]) => {
    if (!props.header.anchor || hash !== `#${props.header.anchor}`) {
      followed = ''
      return
    }
    if (wantLive === '1' && hasLive) liveOpen.value = true
    const key = `${hash}|${loaded}`
    if (followed === key) return
    followed = key
    more.value = true
    void nextTick(() => el.value?.scrollIntoView({ block: 'start' }))
  },
  { immediate: true },
)

// #event-<id> (R4 of "Desk: a notification click opens the event it is
// about"): the thread that holds the event opens Show N more and the group
// around it, then scrolls the card into view, focuses it and highlights it
// for highlightMs. Once per hash, also when the items arrive after it (the
// ?open= landing); a thread without the event, or an event not loaded,
// leaves the page as it is.
const highlightMs = 2000
let followedEvent = ''
let highlight: ReturnType<typeof setTimeout> | undefined
watch(
  () => [route.hash, props.thread.items] as const,
  ([hash, items]) => {
    const id = /^#event-(\d+)$/.exec(hash)?.[1]
    if (!id) {
      followedEvent = ''
      return
    }
    if (followedEvent === hash) return
    const place = eventPlace(items, Number(id))
    if (!place) return
    followedEvent = hash
    if (place.index >= newest) more.value = true
    if (place.group) setOpen(place.group, true)
    void nextTick(() => showEvent(`event-${id}`, 10))
  },
  { immediate: true },
)
// showEvent waits up to frames animation frames for the opened
// collapsibles to render the card.
function showEvent(anchor: string, frames: number) {
  const card = document.getElementById(anchor)
  if (!card) {
    if (frames > 0) requestAnimationFrame(() => showEvent(anchor, frames - 1))
    return
  }
  card.scrollIntoView({ block: 'center' })
  card.focus({ preventScroll: true })
  clearTimeout(highlight)
  document.querySelector('.event-highlight')?.classList.remove('event-highlight')
  card.classList.add('event-highlight')
  highlight = setTimeout(() => card.classList.remove('event-highlight'), highlightMs)
}
onBeforeUnmount(() => clearTimeout(highlight))
</script>

<template>
  <section ref="el" :id="header.anchor" :aria-label="header.label" class="thread scroll-mt-3" data-cockpit-thread>
    <header class="thread-head">
      <h2 class="thread-title">
        {{ header.ref }}<template v-if="header.title"> · {{ header.title }}</template>
      </h2>
      <Badge v-if="header.state" :tone="header.state.tone" data-cockpit-thread-state>{{ header.state.label }}</Badge>
      <span v-if="header.step" class="step" data-cockpit-thread-step>{{ header.step }}</span>
      <span v-if="header.milestoneTitle" class="faint milestone" data-cockpit-thread-milestone>{{ header.milestoneTitle }}</span>
    </header>
    <UCollapsible v-if="live" v-model:open="liveOpen" data-cockpit-thread-live>
      <template #default="{ open }">
        <UButton
          color="neutral"
          variant="ghost"
          size="sm"
          icon="i-lucide-activity"
          label="Live transcript"
          :trailing-icon="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
        />
      </template>
      <template #content>
        <LiveTranscript class="transcript" :namespace="thread.namespace" :name="thread.project" :task="live.task" :pod="live.pod" :run="live.run" />
      </template>
    </UCollapsible>
    <ol class="items">
      <li v-for="item in shown" :key="itemKey(item)">
        <CockpitItem
          :item="item"
          :open="isOpen(item)"
          :project="project"
          :prompts="prompts"
          :can-steer="canSteer"
          @update:open="item.type === 'group' && setOpen(item, $event)"
          @posted="emit('posted')"
        />
      </li>
    </ol>
    <UCollapsible v-if="rest.length" v-model:open="more" data-cockpit-more>
      <template #default="{ open }">
        <UButton
          color="neutral"
          variant="link"
          size="sm"
          :label="open ? 'Show fewer' : `Show ${rest.length} more`"
          :trailing-icon="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
        />
      </template>
      <template #content>
        <ol class="items rest">
          <li v-for="item in rest" :key="itemKey(item)">
            <CockpitItem
              :item="item"
              :open="isOpen(item)"
              :project="project"
              :prompts="prompts"
              :can-steer="canSteer"
              @update:open="item.type === 'group' && setOpen(item, $event)"
              @posted="emit('posted')"
            />
          </li>
        </ol>
      </template>
    </UCollapsible>
  </section>
</template>

<style scoped>
.thread {
  display: grid;
  gap: 8px;
  margin-top: 12px;
  padding: 10px 12px 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
}
.thread-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 8px;
  min-width: 0;
}
.thread-title {
  margin: 0;
  min-width: 0;
  font-size: 13.5px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.step,
.milestone {
  font-size: 12.5px;
}
.milestone {
  margin-left: auto;
}
.items {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.rest {
  padding-top: 10px;
}
.transcript {
  margin: 4px 0 8px;
}
</style>
