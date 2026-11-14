<script setup lang="ts">
import { holdLines } from '../finishing'
import type { FinishingEntry } from '../generated/views'
import ExtLink from './ExtLink.vue'

// ProjectFinishing: done milestones still open on GitLab and what holds
// each (Decision 5, docs/ui.md). Titles are text.
defineProps<{ namespace: string; name: string; finishing: FinishingEntry[]; gitlab: string }>()
</script>

<template>
  <section v-if="finishing.length" class="grid gap-1 text-[13px]" aria-labelledby="project-finishing">
    <h2 id="project-finishing" class="m-0 text-sm font-semibold">Finishing</h2>
    <ul class="m-0 grid list-none gap-0.5 p-0">
      <li v-for="e in finishing" :key="e.iid" class="grid gap-0.5 rounded-md px-1.5 py-1 hover:bg-(--surface-2)" data-finishing-entry>
        <RouterLink :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(e.iid) } }" class="[overflow-wrap:anywhere]">
          <span class="font-mono text-xs">%{{ e.iid }}</span> {{ e.title }}
        </RouterLink>
        <div v-for="(h, i) in holdLines(e, gitlab)" :key="i" class="ps-4 text-xs text-(--text-3) [overflow-wrap:anywhere]">
          <RouterLink v-if="h.retro" :to="{ name: 'stream-milestone', params: { namespace, name, iid: String(e.iid) }, query: { tab: 'retrospective' } }">{{ h.text }}</RouterLink>
          <ExtLink v-else-if="h.href" :href="h.href">{{ h.text }}</ExtLink>
          <template v-else>{{ h.text }}</template>
          <template v-for="l in h.links" :key="l.text">{{ ' ' }}<ExtLink :href="l.href">{{ l.text }}</ExtLink></template>
        </div>
      </li>
    </ul>
  </section>
</template>
