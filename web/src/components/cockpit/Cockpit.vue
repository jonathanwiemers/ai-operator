<script setup lang="ts">
import UAlert from '@nuxt/ui/components/Alert.vue'
import UButton from '@nuxt/ui/components/Button.vue'
import UDashboardToolbar from '@nuxt/ui/components/DashboardToolbar.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import USwitch from '@nuxt/ui/components/Switch.vue'
import { computed, provide, ref } from 'vue'
import { alertColor } from '../../badgeTone'
import { getDecisions } from '../../client'
import { cockpitDecisionsKey, decisionsByID } from '../../cockpitDecision'
import { threadHeader, type ThreadHeaderContext } from '../../cockpitThread'
import { useNow, useResource } from '../../live'
import { searchToolbarUi } from '../../pageHeader'
import type { ProjectDetail, PromptView } from '../../types'
import { useCockpitStream, type CockpitStreamScope } from '../../useCockpitStream'
import CockpitConversation from './CockpitConversation.vue'
import CockpitThread from './CockpitThread.vue'

// Cockpit is a Cockpit tab (R3 of "Desk: the Cockpit newest first by
// issue"), top to bottom: the composer slot (a milestone's
// CockpitComposer; its sent(id) reloads the stream and waits for the
// pilot's answer to message id (expectAnswer), reload() only reloads, after
// a /pilot note), the latest exchange with the pilot, the pinned cards (the
// pinned slot; a milestone's MilestoneNeeds), a toolbar with Expand all and
// the search,
// the threads of cockpitThreads, newest on top, each a CockpitThread card
// (R4), and Load older last. headers holds what the thread headers need
// (threadHeader): the milestone's issue rows, queue items and tasks, the
// project's milestone titles, the milestone's status, and the transcript
// grant; the Overview passes none.
const props = defineProps<{
  scope: CockpitStreamScope
  project?: ProjectDetail | null
  prompts?: PromptView[]
  canSteer?: boolean
  headers?: Omit<ThreadHeaderContext, 'scope' | 'now'> & { transcript?: boolean }
}>()

const stream = useCockpitStream(() => props.scope)
const { scopeKind, conversation, threads, older, historyOn, loading, loadingOlder, error } = stream
const now = useNow()

// The decisions the decision items revisit in place (R5, D819-P1): one load
// per Cockpit, of the milestone, or of the whole project (milestone 0) so
// every milestone's decisions resolve; the Overview loads none.
const decisions = useResource(() => {
  const { namespace, name, milestoneIID } = props.scope
  if (!namespace || !name) return null
  return () => getDecisions(namespace, name, { milestone: milestoneIID ?? 0, q: '', state: 'all' })
}, ['task'])
provide(
  cockpitDecisionsKey,
  computed(() => decisionsByID(decisions.data.value?.decisions)),
)

const qDraft = ref('')
function applySearch() {
  stream.search(qDraft.value)
}

// Expand all opens every thread fully and every group (CockpitThread).
const expandAll = ref(false)
</script>

<template>
  <div class="cockpit" data-cockpit>
    <slot name="composer" :sent="stream.expectAnswer" :reload="stream.reload" />
    <CockpitConversation :exchanges="conversation" />

    <!-- #pinned: an Inbox proposal without a prompt lands here (inboxRoute). -->
    <section id="pinned" aria-label="Pinned" class="scroll-mt-3" data-cockpit-pinned>
      <slot name="pinned" />
    </section>

    <section aria-label="Stream" data-cockpit-stream>
      <UDashboardToolbar class="mt-3" :ui="searchToolbarUi" data-tab-toolbar>
        <template #left>
          <!-- On phones the switch is a 44 px target (style.css, "Hit areas"):
               its container grows to it, so it stays in its toolbar row. -->
          <USwitch
            v-model="expandAll"
            label="Expand all"
            :ui="{ root: 'items-center tablet:items-start', container: 'min-h-11 tablet:min-h-0' }"
            data-cockpit-expand
          />
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
      <div v-if="loading && !threads.length && !conversation.length" class="empty">Loading…</div>
      <UEmpty v-else-if="!threads.length && !conversation.length" icon="i-lucide-activity" title="No events yet." variant="naked" size="sm" />
      <CockpitThread
        v-for="t in threads"
        :key="t.key"
        :thread="t"
        :header="threadHeader(t, { ...headers, scope: scopeKind, now })"
        :expand-all="expandAll"
        :transcript="headers?.transcript"
        :project="project"
        :prompts="prompts"
        :can-steer="canSteer"
        @posted="stream.reload"
      />
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
    </section>
  </div>
</template>

<style scoped>
.cockpit {
  display: grid;
  gap: 8px;
  min-width: 0;
}
.older {
  margin: 16px auto 4px;
  display: flex;
}
</style>
