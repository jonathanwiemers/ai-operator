<script setup lang="ts">
import Markdown from './Markdown.vue'
import { computed } from 'vue'
import { runLine } from '../format'
import { newsflash } from '../newsflash'
import BlockList from './blocks/BlockList.vue'
import ItemsBlock from './blocks/ItemsBlock.vue'
import type { ResearchDetail, ResearchResult, ResearchRun } from '../types'

// A stored result in the research's view: a news flash groups its items by
// topic, a report shows its blocks in order. runLink: the run line links to
// the run's page.
const props = defineProps<{
  research: ResearchDetail
  run: ResearchRun | null
  result: ResearchResult
  runLink?: boolean
}>()

const line = computed(() => {
  const r = props.run
  if (!r) return ''
  const end = r.finishedAt ? Date.parse(r.finishedAt) : NaN
  return runLine(r.engine, r.model, Number.isNaN(end) ? undefined : end - Date.parse(r.startedAt), r.startedAt)
})

const flash = computed(() =>
  props.research.view === 'newsflash' ? newsflash(props.result.blocks, props.research.topics) : null,
)
</script>

<template>
  <article class="flex max-w-[920px] min-w-0 flex-col gap-[18px]">
    <div v-if="result.warnings?.length" class="banner warning [overflow-wrap:anywhere]">
      <span v-for="(w, i) in result.warnings" :key="i">{{ w }}</span>
    </div>
    <header class="flex flex-col gap-1">
      <h2 class="text-xl [overflow-wrap:anywhere]">{{ result.title }}</h2>
      <div v-if="line" class="text-[13px] text-(--text-3)" data-run-line>
        <RouterLink
          v-if="runLink && run"
          :to="`/research/${encodeURIComponent(research.namespace)}/${encodeURIComponent(research.name)}/runs/${encodeURIComponent(run.id)}`"
          class="inline-flex min-h-11 items-center text-inherit tablet:min-h-0"
          :title="run.choiceReason"
        >
          {{ line }}
        </RouterLink>
        <span v-else>{{ line }}</span>
        <span v-if="run && run.status !== 'succeeded'"> · {{ run.status }}</span>
      </div>
      <Markdown v-if="result.summary" class="mt-1.5 text-[15px] text-(--text-2)" :text="result.summary" />
    </header>

    <template v-if="flash">
      <section v-for="g in flash.groups" :key="g.topic" class="min-w-0">
        <h3 class="mb-2">{{ g.topic || 'Other' }} <span class="ms-1 font-medium text-(--text-3)">{{ g.items.length }}</span></h3>
        <ItemsBlock :items="g.items" hide-topic />
      </section>
      <BlockList v-if="flash.rest.length" :blocks="flash.rest" class="mt-2" />
    </template>
    <BlockList v-else :blocks="result.blocks" />
  </article>
</template>
