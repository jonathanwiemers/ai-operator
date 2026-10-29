<script setup lang="ts">
import { computed, ref } from 'vue'
import { useNow, useResource } from '../live'
import { clockTime } from '../format'
import Badge from '../components/Badge.vue'
import RelTime from '../components/RelTime.vue'
import ResearchResult from '../components/ResearchResult.vue'
import NotFound from './NotFound.vue'
import type { ResearchResponse } from '../types'

const props = defineProps<{ namespace: string; name: string }>()
const { data, error, notFound, loading } = useResource<ResearchResponse>(
  () => `/researches/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}`,
  ['research'],
)
const now = useNow()
const r = computed(() => data.value?.research)
const agent = computed(() => {
  const a = r.value?.agent
  if (!a) return 'auto'
  return `${a.engine}/${a.model}${a.effort ? ` (${a.effort})` : ''}`
})
const showPrompt = ref(false)
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <nav class="crumbs muted">
      <RouterLink to="/research">Research</RouterLink>
      <span>/</span>
      <span>{{ name }}</span>
    </nav>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !r" class="card empty">Loading…</div>

    <template v-if="r && data">
      <header class="head">
        <div class="row">
          <h1>{{ r.name }}</h1>
          <span class="ns faint">{{ r.namespace }}</span>
          <span class="spacer" />
          <Badge>{{ r.view === 'newsflash' ? 'News flash' : 'Report' }}</Badge>
          <Badge tone="muted">{{ r.depth }}</Badge>
          <Badge v-if="r.phase === 'Running'" tone="progress" dot pulse>Running</Badge>
        </div>
        <p class="prompt prose" :class="{ clamped: !showPrompt }">{{ r.prompt }}</p>
        <button v-if="r.prompt.length > 240" type="button" class="more" @click="showPrompt = !showPrompt">
          {{ showPrompt ? 'Show less' : 'Show the whole prompt' }}
        </button>
        <div class="facts-line muted">
          <span v-if="r.schedule"><code>{{ r.schedule }}</code> {{ r.timeZone || 'UTC' }}</span>
          <span v-else>one-off<template v-if="r.createdBy"> by {{ r.createdBy }}</template></span>
          <span>agent <code>{{ agent }}</code></span>
          <span>last run <RelTime :time="r.lastRun" /></span>
          <span v-if="r.nextRun">next run <RelTime :time="r.nextRun" /></span>
        </div>
        <div v-if="r.waiting || r.skippedReason" class="waiting">
          <Badge v-if="r.waiting" tone="warning">
            waiting for <code>{{ r.waiting.engine }}</code> until {{ clockTime(r.waiting.until, now) }}
          </Badge>
          <span v-if="r.skippedReason" class="muted">{{ r.skippedReason }}</span>
        </div>
        <div v-if="r.run?.outcome === 'failed' && r.run.message" class="banner danger">
          <strong>The last run failed</strong>
          <span>{{ r.run.message }}</span>
        </div>
      </header>

      <ResearchResult v-if="data.result" :research="r" :run="data.run" :result="data.result" run-link class="section" />
      <div v-else class="card empty section">
        {{ r.phase === 'Running' ? 'The first run is in progress.' : 'No result yet.' }}
      </div>
    </template>
  </div>
</template>

<style scoped>
.crumbs {
  display: flex;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 14px;
}

.head {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.ns {
  font-size: 12px;
}

.prompt {
  margin: 4px 0 0;
  max-width: 920px;
  color: var(--text-2);
}

.prompt.clamped {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  overflow: hidden;
}

.more {
  align-self: flex-start;
  padding: 0;
  border: none;
  background: none;
  color: var(--link);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.facts-line {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 18px;
  font-size: 13px;
}

.waiting {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}
</style>
