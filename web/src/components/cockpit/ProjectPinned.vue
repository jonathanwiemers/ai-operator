<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed } from 'vue'
import { getMilestone } from '../../client'
import { useResource } from '../../live'
import { needsOpen } from '../../needsOpen'
import type { InboxItem, MilestoneView, ProjectDetail } from '../../types'
import MilestoneNeeds from '../MilestoneNeeds.vue'

// ProjectPinned is the project Cockpit's pinned cards (R15 of "Desk: the
// Cockpit", D790-P1): for every milestone with open needs, its
// MilestoneNeeds under the milestone's name, answered in place, and a link
// to the milestone's Cockpit for talking to its pilot (the project page has
// no composer, D9). It loads the view of every milestone whose autopilot is
// not done (running, paused, off or waiting: a stopped milestone can still
// hold a proposal or a question) and of those the project's Inbox items
// name, in the project's order (newest first); a milestone shows once its
// view has open needs (needsOpen). Done milestones and those without an
// autopilot have no pilot and no queue, so nothing of theirs is pinned
// unless the Inbox names them.
const props = defineProps<{ namespace: string; name: string; project: ProjectDetail | null; inbox: InboxItem[] }>()

const candidates = computed(() => {
  const named = new Set(props.inbox.map((i) => i.milestoneIID).filter((iid): iid is number => !!iid))
  const listed = (props.project?.milestones ?? []).filter((m) => {
    const phase = m.spec?.status.autopilot?.phase
    return named.delete(m.iid) || (!!phase && phase !== 'Done')
  })
  return [...listed.map((m) => m.iid), ...named]
})

// The source reads only strings, so a live reload of the project (a new
// object, the same milestones) does not drop and remount the cards; live
// events reload the views in place.
const key = computed(() => (props.project ? candidates.value.join(',') : null))
const views = useResource(() => {
  const { namespace, name } = props
  const k = key.value
  if (k == null) return null
  const iids = k ? k.split(',').map(Number) : []
  return () => Promise.allSettled(iids.map((iid) => getMilestone(namespace, name, iid)))
}, ['project', 'task'])

const pinned = computed((): MilestoneView[] =>
  (views.data.value ?? []).flatMap((r) => (r.status === 'fulfilled' && needsOpen(r.value).count ? [r.value] : [])),
)

const cockpitTo = (m: MilestoneView) => ({
  name: 'stream-milestone' as const,
  params: { namespace: props.namespace, name: props.name, iid: String(m.iid) },
})
</script>

<template>
  <div class="grid min-w-0 gap-6" data-project-pinned>
    <div v-if="!views.data.value" class="empty">Loading…</div>
    <UEmpty v-else-if="!pinned.length" icon="i-lucide-inbox" title="Nothing needs you." variant="naked" size="sm" />
    <section
      v-for="m in pinned"
      :key="m.iid"
      class="grid min-w-0 gap-3"
      :aria-labelledby="`pinned-milestone-${m.iid}`"
      :data-pinned-milestone="m.iid"
    >
      <div class="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 :id="`pinned-milestone-${m.iid}`" class="m-0 min-w-0 text-[15px] font-semibold [overflow-wrap:anywhere]">
          {{ m.title || `%${m.iid}` }}
        </h2>
        <span class="faint text-[12.5px] whitespace-nowrap">%{{ m.iid }}</span>
        <UButton
          :to="cockpitTo(m)"
          color="neutral"
          variant="link"
          size="sm"
          class="ms-auto min-h-11 tablet:min-h-0"
          trailing-icon="i-lucide-arrow-right"
          :label="m.pilot ? 'Talk to the pilot' : 'Open its Cockpit'"
          :aria-label="`${m.pilot ? 'Talk to the pilot in' : 'Open'} the Cockpit of ${m.title || `%${m.iid}`}`"
          data-pinned-cockpit
        />
      </div>
      <MilestoneNeeds :namespace="namespace" :name="name" :milestone="m" />
    </section>
  </div>
</template>
