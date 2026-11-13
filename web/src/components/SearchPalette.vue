<script setup lang="ts">
// The ⌘K palette, loaded by App.vue on its first open.
// Projects come from the rail, milestones load once, issues and merge
// requests come from GET /search while typing. The items are links: Enter
// or a click navigates and the select closes the palette; useBackClose in
// App.vue lets the navigation wait until the palette's history entry went.
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import UCommandPalette from '@nuxt/ui/components/CommandPalette.vue'
import UModal from '@nuxt/ui/components/Modal.vue'
import { getMilestones, getSearch } from '../client'
import { paletteGroups, searchQuery } from '../searchItems'
import { paletteOpen } from '../searchPalette'
import type { Feature, MilestoneRow, ProjectSummary, SearchResult } from '../generated/views'

const props = defineProps<{ projects: ProjectSummary[] | null; features: Feature[] | null; fullscreen: boolean }>()

const milestones = ref<MilestoneRow[]>([])
getMilestones({ state: 'all' })
  .then((r) => (milestones.value = r.milestones ?? []))
  .catch(() => undefined)

// GET /search 200 ms after the last keystroke. Answers to an older term are
// dropped; errors and short terms leave the group empty.
const term = ref('')
const results = ref<SearchResult[]>([])
let timer: number | undefined
let seq = 0
watch(term, (t) => {
  window.clearTimeout(timer)
  const n = ++seq
  const q = searchQuery(t)
  if (q === null) return void (results.value = [])
  timer = window.setTimeout(() => {
    getSearch(q)
      .then((r) => r.results ?? [])
      .catch(() => [])
      .then((r) => n === seq && (results.value = r))
  }, 200)
})
onBeforeUnmount(() => window.clearTimeout(timer))
// Each open starts empty; this component stays mounted after the first.
watch(paletteOpen, (v) => v || (term.value = ''))

const groups = computed(() =>
  paletteGroups({
    projects: props.projects ?? [],
    milestones: milestones.value,
    results: results.value,
    features: props.features ?? [],
  }),
)
</script>

<template>
  <UModal
    v-model:open="paletteOpen"
    title="Search"
    description="Projects, milestones, issues and merge requests"
    :fullscreen="fullscreen"
    :ui="{ overlay: 'z-40', content: 'z-40 tablet:max-w-xl' }"
  >
    <template #content>
      <UCommandPalette
        v-model:search-term="term"
        :groups="groups"
        placeholder="Search projects, milestones, issues, !MR"
        :close="{ class: 'min-h-11 min-w-11 justify-center tablet:min-h-0 tablet:min-w-0' }"
        close-icon="i-lucide-x"
        class="h-full tablet:h-auto tablet:max-h-[min(32rem,80dvh)]"
        data-search-palette
        @update:model-value="paletteOpen = false"
        @update:open="paletteOpen = $event"
      />
    </template>
  </UModal>
</template>
