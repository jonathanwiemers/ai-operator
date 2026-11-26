<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import UButton from '@nuxt/ui/components/Button.vue'
import UCheckbox from '@nuxt/ui/components/Checkbox.vue'
import UDropdownMenu, { type DropdownMenuItem } from '@nuxt/ui/components/DropdownMenu.vue'
import UInput from '@nuxt/ui/components/Input.vue'
import UTable from '@nuxt/ui/components/Table.vue'
import { useToast } from '@nuxt/ui/composables/useToast'
import { computed, ref, watch } from 'vue'
import type { ChatCommand } from '../chatState'
import { getLooseIssues } from '../client'
import type { LooseIssue, LooseItem } from '../generated/views'
import { useResource } from '../live'
import { answerToast, filterIssues, itemActions, itemReason, itemState, ownersNote, selectionOrder, takeCommand } from '../looseIssues'
import { useCommandSend } from '../useCommandSend'
import Badge from './Badge.vue'
import ExtLink from './ExtLink.vue'
import RelTime from './RelTime.vue'

// LooseIssues: a project's Loose issues tab (R8 of "Loose issues"): the
// queue with a ⋯ menu per unfinished row, and the open issues without a
// milestone to hand to autopilot. Commands go to the Queue task's chat;
// their answer shows as a toast, then the view reloads. Titles, labels and
// reasons are text.
const props = defineProps<{ namespace: string; name: string }>()

const { data, error, loading, reload } = useResource(() => {
  const { namespace, name } = props
  return () => getLooseIssues(namespace, name)
}, ['task'])

const items = computed(() => data.value?.items ?? [])
const issues = computed(() => data.value?.issues ?? [])
const take = computed(() => !!data.value?.take && !!data.value.queueTask)

const search = ref('')
const shown = computed(() => filterIssues(issues.value, search.value))
const rowSelection = ref<Record<string, boolean>>({})
// order: the ticked IIDs in the order they were ticked (the take's order).
const order = ref<number[]>([])
watch([rowSelection, issues], () => (order.value = selectionOrder(order.value, rowSelection.value, issues.value)), { deep: true })

const cmds = useCommandSend(() => ({ namespace: props.namespace, name: props.name }))
const toast = useToast()

async function send(cmd: ChatCommand | null, key: string) {
  if (!cmd || !(await cmds.send(data.value?.queueTask, cmd, key))) {
    if (cmds.error.value) toast.add({ title: 'The command was not sent', description: cmds.error.value, color: 'error' })
    return
  }
  if (cmd.cmd === 'take') {
    rowSelection.value = {}
    order.value = []
  }
}
watch(cmds.answer, (a) => {
  const t = answerToast(a)
  if (!t) return
  toast.add(t)
  void reload()
})

function menu(it: LooseItem): DropdownMenuItem[] {
  return itemActions(it).map((a) => ({ label: a.label, disabled: !!cmds.sending.value, onSelect: () => void send(a.cmd, `${a.id}:${it.iid}`) }))
}

const itemColumns = computed((): TableColumn<LooseItem>[] => [
  { accessorKey: 'iid', header: 'Issue' },
  { accessorKey: 'title', header: 'Title' },
  { id: 'state', header: 'State' },
  { id: 'reason', header: 'Reason' },
  { id: 'mr', header: 'MR' },
  { accessorKey: 'mergedAt', header: 'Merged' },
  ...(take.value ? [{ id: 'actions', header: 'Actions' }] : []),
])
const issueColumns = computed((): TableColumn<LooseIssue>[] => [
  ...(take.value ? [{ id: 'select', header: 'Select' }] : []),
  { accessorKey: 'iid', header: 'Issue' },
  { accessorKey: 'title', header: 'Title' },
  { accessorKey: 'labels', header: 'Labels' },
  { accessorKey: 'author', header: 'Author' },
  { accessorKey: 'updatedAt', header: 'Updated' },
])
const selectable = { enableRowSelection: (row: { original: LooseIssue }) => !row.original.inQueue }
</script>

<template>
  <div class="grid min-w-0 gap-6 text-[13px]">
    <div v-if="error" class="text-(--text-3)" role="alert">{{ error }}</div>
    <div v-else-if="loading && !data" class="empty">Loading…</div>
    <template v-else-if="data">
      <p v-if="!take" class="m-0 text-(--text-2)" data-loose-owners>{{ ownersNote(data.ownerSubjects) }}</p>

      <section class="grid min-w-0 gap-2" aria-labelledby="loose-queue">
        <h2 id="loose-queue" class="m-0 text-sm font-semibold">Queue</h2>
        <p class="m-0 text-(--text-2) [overflow-wrap:anywhere]" data-loose-queue-state>
          <template v-if="!data.queue">The queue starts once the operator has created its task.</template>
          <template v-else>
            <template v-if="data.queue.frozenAt">
              Frozen since <RelTime :time="data.queue.frozenAt" />: the default branch is red, nothing starts or merges.
            </template>
            <template v-else-if="data.queue.phase === 'Paused'">Paused{{ data.queue.pausedReason ? `: ${data.queue.pausedReason}` : '.' }}</template>
            <template v-else>Running.</template>
            Up to {{ data.queue.maxParallel }} at a time.
          </template>
        </p>
        <UTable :data="items" :columns="itemColumns" :get-row-id="(r: LooseItem) => String(r.iid)" empty="No issues in the queue yet." data-loose-queue>
          <template #iid-cell="{ row }">
            <ExtLink :href="row.original.webURL" class="font-mono text-xs">#{{ row.original.iid }}</ExtLink>
          </template>
          <template #title-cell="{ row }">
            <span class="[overflow-wrap:anywhere] whitespace-normal">{{ row.original.title }}</span>
          </template>
          <template #state-cell="{ row }">
            <Badge :tone="itemState(row.original, items).tone" :data-loose-state="row.original.iid">{{ itemState(row.original, items).label }}</Badge>
          </template>
          <template #reason-cell="{ row }">
            <span class="[overflow-wrap:anywhere] whitespace-normal text-(--text-2)">{{ itemReason(row.original) }}</span>
          </template>
          <template #mr-cell="{ row }">
            <ExtLink v-if="row.original.mergeRequestURL" :href="row.original.mergeRequestURL" class="font-mono text-xs">!{{ row.original.mergeRequestIID }}</ExtLink>
          </template>
          <template #mergedAt-cell="{ row }">
            <RelTime v-if="row.original.mergedAt" :time="row.original.mergedAt" />
          </template>
          <template #actions-header><span class="sr-only">Actions</span></template>
          <template #actions-cell="{ row }">
            <UDropdownMenu v-if="itemActions(row.original).length" :items="menu(row.original)" :content="{ align: 'end' }" :ui="{ content: 'z-40', item: 'min-h-11 tablet:min-h-0' }">
              <UButton
                color="neutral"
                variant="outline"
                size="xs"
                icon="i-lucide-ellipsis"
                :loading="cmds.sending.value.endsWith(`:${row.original.iid}`)"
                :aria-label="`Actions for #${row.original.iid}`"
                :data-loose-menu="row.original.iid"
              />
            </UDropdownMenu>
          </template>
        </UTable>
      </section>

      <section class="grid min-w-0 gap-2" aria-labelledby="loose-open">
        <h2 id="loose-open" class="m-0 text-sm font-semibold">Issues without a milestone</h2>
        <div class="flex flex-wrap items-center gap-2">
          <UButton
            v-if="take"
            :label="`Hand to autopilot (${order.length})`"
            :disabled="!order.length || !!cmds.sending.value"
            :loading="cmds.sending.value === 'take'"
            data-loose-take
            @click="send(takeCommand(order), 'take')"
          />
          <UInput v-model="search" class="ml-auto w-full max-w-64" icon="i-lucide-search" placeholder="Search issues" aria-label="Search issues" data-loose-search />
        </div>
        <UTable
          v-model:row-selection="rowSelection"
          :data="shown"
          :columns="issueColumns"
          :get-row-id="(r: LooseIssue) => String(r.iid)"
          :row-selection-options="selectable"
          :empty="search ? 'No issue matches the search.' : 'No open issue without a milestone.'"
          data-loose-issues
        >
          <template #select-header="{ table }">
            <UCheckbox
              :model-value="table.getIsSomePageRowsSelected() ? 'indeterminate' : table.getIsAllPageRowsSelected()"
              aria-label="Select all"
              @update:model-value="(v: boolean | 'indeterminate') => table.toggleAllPageRowsSelected(!!v)"
            />
          </template>
          <template #select-cell="{ row }">
            <UCheckbox
              :model-value="row.getIsSelected()"
              :disabled="!row.getCanSelect()"
              :aria-label="`Select #${row.original.iid}`"
              :data-loose-select="row.original.iid"
              @update:model-value="(v: boolean | 'indeterminate') => row.toggleSelected(!!v)"
            />
          </template>
          <template #iid-cell="{ row }">
            <ExtLink :href="row.original.webURL" class="font-mono text-xs">#{{ row.original.iid }}</ExtLink>
          </template>
          <template #title-cell="{ row }">
            <span class="[overflow-wrap:anywhere] whitespace-normal">{{ row.original.title }}</span>
            <Badge v-if="row.original.inQueue" tone="info" class="ms-2" :data-loose-in-queue="row.original.iid">in queue</Badge>
          </template>
          <template #labels-cell="{ row }">
            <span class="flex flex-wrap gap-1">
              <Badge v-for="l in row.original.labels" :key="l">{{ l }}</Badge>
            </span>
          </template>
          <template #updatedAt-cell="{ row }">
            <RelTime :time="row.original.updatedAt" />
          </template>
        </UTable>
      </section>
    </template>
  </div>
</template>
