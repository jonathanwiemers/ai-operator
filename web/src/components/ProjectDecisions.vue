<script setup lang="ts">
import USelect from '@nuxt/ui/components/Select.vue'
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { decisionsQuery, milestoneFromQuery } from '../decisions'
import MilestoneDecisions from './MilestoneDecisions.vue'

// A project's Decisions tab (R6 of "Desk: the Cockpit newest first by
// issue"): MilestoneDecisions across the project's milestones, with the
// milestone select (?milestone=, 0 all) in the tab's toolbar (D817-P3). A
// milestone the list lacks (a stored one) shows as %<iid>.
const props = defineProps<{
  namespace: string
  name: string
  milestones: readonly { iid: number; title: string }[]
}>()

const route = useRoute()
const router = useRouter()
const milestone = computed(() => milestoneFromQuery(route.query.milestone))
const items = computed(() => {
  const all = [{ value: 0, label: 'All milestones' }, ...props.milestones.map((m) => ({ value: m.iid, label: m.title || `%${m.iid}` }))]
  const iid = milestone.value
  return iid && !all.some((i) => i.value === iid) ? [...all, { value: iid, label: `%${iid}` }] : all
})
function setMilestone(iid: number) {
  void router.replace({ query: decisionsQuery(route.query, { milestone: iid }) })
}
</script>

<template>
  <MilestoneDecisions :namespace="namespace" :name="name" :milestone="milestone" :milestones="milestones">
    <template #controls>
      <USelect
        :model-value="milestone"
        :items="items"
        aria-label="Milestone"
        trailing-icon="i-lucide-chevron-down"
        selected-icon="i-lucide-check"
        class="min-h-11 w-full tablet:min-h-0 tablet:w-56"
        :ui="{ base: 'tablet:text-sm/5', item: 'min-h-11 tablet:min-h-0' }"
        data-decisions-milestone
        @update:model-value="setMilestone(Number($event))"
      />
    </template>
  </MilestoneDecisions>
</template>
