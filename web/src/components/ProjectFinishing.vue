<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { reactive, ref } from 'vue'
import { ApiError } from '../client'
import { closeHoldingIssue } from '../closeHoldingIssue'
import { holdLines } from '../finishing'
import type { FinishingEntry } from '../generated/views'
import ExtLink from './ExtLink.vue'

// Done milestones still open (Decision 5). Close issue: user's token; live reload clears the row.
const props = defineProps<{ namespace: string; name: string; finishing: FinishingEntry[]; gitlab: string }>()
const confirm = ref('')
const busy = ref('')
const done = reactive<Record<string, true>>({})
const err = reactive<Record<string, string>>({})
const k = (m: number, i: number) => `${m}:${i}`

async function close(m: number, i: number) {
  const id = k(m, i)
  if (busy.value) return
  busy.value = id
  err[id] = ''
  try {
    await closeHoldingIssue(props.namespace, props.name, m, i)
    done[id] = true
    confirm.value = ''
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    err[id] = e instanceof ApiError && e.data.login ? `${msg} Connect GitLab again.` : msg
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <section v-if="finishing.length" class="grid gap-1 text-[13px]" aria-labelledby="project-finishing">
    <h2 id="project-finishing" class="m-0 text-sm font-semibold">Finishing</h2>
    <ul class="m-0 grid list-none gap-0.5 p-0">
      <li v-for="e in finishing" :key="e.iid" class="grid gap-0.5 rounded-md px-1.5 py-1 hover:bg-(--surface-2)" data-finishing-entry>
        <RouterLink :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(e.iid) } }" class="[overflow-wrap:anywhere]">
          <span class="font-mono text-xs">%{{ e.iid }}</span> {{ e.title }}
        </RouterLink>
        <div v-for="(h, i) in holdLines(e, gitlab)" :key="i" class="flex flex-wrap items-baseline gap-x-2 gap-y-1 ps-4 text-xs text-(--text-3) [overflow-wrap:anywhere]">
          <span>
            <RouterLink v-if="h.retro" :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(e.iid) }, query: { tab: 'retrospective' } }">{{ h.text }}</RouterLink>
            <ExtLink v-else-if="h.href" :href="h.href">{{ h.text }}</ExtLink>
            <template v-else>{{ h.text }}</template>
            <template v-for="l in h.links" :key="l.text">{{ ' ' }}<ExtLink :href="l.href">{{ l.text }}</ExtLink></template>
          </span>
          <template v-if="h.issue != null">
            <span v-if="done[k(e.iid, h.issue)]" role="status">Closed; autopilot closes the milestone on its next poll.</span>
            <template v-else-if="confirm === k(e.iid, h.issue)">
              Close #{{ h.issue }}?
              <UButton color="neutral" variant="solid" size="xs" :disabled="!!busy" :label="busy === k(e.iid, h.issue) ? '…' : 'Yes'" @click="close(e.iid, h.issue)" />
              <UButton color="neutral" variant="ghost" size="xs" :disabled="!!busy" label="Cancel" @click="confirm = ''" />
            </template>
            <UButton v-else color="neutral" variant="outline" size="xs" :disabled="!!busy" label="Close issue" @click="confirm = k(e.iid, h.issue)" />
            <span v-if="err[k(e.iid, h.issue)]" role="alert">{{ err[k(e.iid, h.issue)] }}</span>
          </template>
        </div>
      </li>
    </ul>
  </section>
</template>
