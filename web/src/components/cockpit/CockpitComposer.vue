<script setup lang="ts">
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed } from 'vue'
import { useChatSend } from '../../chatSend'
import { cost } from '../../format'
import { slashSuggestions } from '../../stream'
import { useStreamNote } from '../../streamNote'
import type { MilestoneView, ProjectDetail } from '../../types'
import Badge from '../Badge.vue'
import Composer, { type ComposerSend } from '../Composer.vue'

// CockpitComposer is the top of a milestone's Cockpit (R12 of "Desk: the
// Cockpit"; first since "the Cockpit newest first by issue", D1): the pilot line (autonomy, cost, Pilot task) as its header, then
// the Desk chat to the pilot task with the chat grant, else a /pilot note on
// the tracking issue with steer, else a read-only line. The conversation is
// not shown here: the owner's messages and the pilot's answers are the
// Cockpit's conversation under it (CockpitConversation), so after a send it
// emits sent and the Cockpit reloads and waits for the answer. It scrolls
// with the page, on phones too. A stored milestone has none.
const props = defineProps<{ namespace: string; name: string; milestone: MilestoneView; project: ProjectDetail | null }>()
// sent: a chat message went to the pilot task (its id), so the Cockpit
// waits for the answer; posted: a /pilot note went to GitLab, whose reply
// is a comment, so the Cockpit only reloads.
const emit = defineEmits<{ sent: [id: number]; posted: [] }>()

const perms = computed(() => props.milestone.permissions)
const pilot = computed(() => props.milestone.pilot)
const mode = computed(() => (perms.value?.chat ? 'chat' : perms.value?.steer ? 'note' : 'read'))

const { body, sending, sendError, post } = useChatSend(
  () => ({ namespace: props.namespace, name: props.name, task: pilot.value?.task ?? '' }),
  (id) => emit('sent', id),
)
function onSend(s: ComposerSend) {
  if (pilot.value) void post(s.kind, s.body)
}

const note = useStreamNote(
  () => ({
    namespace: props.namespace,
    name: props.name,
    project: props.project,
    milestoneIID: props.milestone.iid,
    steer: true,
    shown: mode.value === 'note',
  }),
  () => emit('posted'),
)

// On phones the Pilot task link is 44 px high; the composer brings its own
// touch sizes.
const tap = 'inline-flex min-h-11 items-center tablet:min-h-0'
</script>

<template>
  <section class="dock" aria-label="Message the pilot" data-cockpit-composer>
    <div v-if="pilot" class="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px]" data-pilot>
      <span class="faint">Pilot</span>
      <Badge :tone="pilot.autonomy === 'managed' ? 'progress' : 'neutral'">{{ pilot.autonomy }}</Badge>
      <span class="faint tabular-nums">{{ cost(pilot.costUSD) }}</span>
      <RouterLink class="ml-auto" :class="tap" :to="{ name: 'task', params: { namespace, name, task: pilot.task } }">Pilot task</RouterLink>
    </div>

    <Composer
      v-if="mode === 'note'"
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
    <UEmpty v-else-if="!pilot" icon="i-lucide-bot" title="No pilot for this milestone." variant="naked" size="sm">
      <template #description>Autopilot, or <code>/pilot coordinate</code> on an issue, starts one.</template>
    </UEmpty>
    <Composer
      v-else-if="mode === 'chat'"
      v-model="body"
      mode="chat"
      placeholder="Message the pilot…"
      hint="Enter+Ctrl/Cmd to send"
      :busy="sending"
      :disabled="sending"
      :error="sendError"
      @send="onSend"
    />
    <p v-else class="m-0 py-2 text-[13px] text-(--text-3)">Read-only: only the milestone's owners can message the pilot.</p>
  </section>
</template>

<style scoped>
.dock {
  display: grid;
  gap: 6px;
  padding: 0 0 10px;
  border-bottom: 1px solid var(--border);
}
</style>
