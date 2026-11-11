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
  <article class="result">
    <div v-if="result.warnings?.length" class="banner warning">
      <span v-for="(w, i) in result.warnings" :key="i">{{ w }}</span>
    </div>
    <header class="head">
      <h2 class="title">{{ result.title }}</h2>
      <div v-if="line" class="run faint">
        <RouterLink
          v-if="runLink && run"
          :to="`/research/${encodeURIComponent(research.namespace)}/${encodeURIComponent(research.name)}/runs/${encodeURIComponent(run.id)}`"
          :title="run.choiceReason"
        >
          {{ line }}
        </RouterLink>
        <span v-else :title="run?.choiceReason">{{ line }}</span>
        <span v-if="run && run.status !== 'succeeded'"> · {{ run.status }}</span>
      </div>
      <Markdown v-if="result.summary" class="summary" :text="result.summary" />
    </header>

    <template v-if="flash">
      <section v-for="g in flash.groups" :key="g.topic" class="topic">
        <h3>{{ g.topic || 'Other' }} <span class="faint count">{{ g.items.length }}</span></h3>
        <ItemsBlock :items="g.items" hide-topic />
      </section>
      <BlockList v-if="flash.rest.length" :blocks="flash.rest" class="rest" />
    </template>
    <BlockList v-else :blocks="result.blocks" />
  </article>
</template>

<style scoped>
.result {
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-width: 920px;
}

.head {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.title {
  font-size: 20px;
  overflow-wrap: anywhere;
}

.run {
  font-size: 13px;
}

.run a {
  color: inherit;
}

.summary {
  margin: 6px 0 0;
  font-size: 15px;
  color: var(--text-2);
}

.topic h3 {
  margin-bottom: 8px;
}

.count {
  margin-left: 4px;
  font-weight: 500;
}

.rest {
  margin-top: 8px;
}
</style>
