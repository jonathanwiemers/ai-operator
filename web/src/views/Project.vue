<script setup lang="ts">
import { computed } from 'vue'
import { useResource } from '../live'
import { cost } from '../format'
import Badge from '../components/Badge.vue'
import EngineUsage from '../components/EngineUsage.vue'
import ExtLink from '../components/ExtLink.vue'
import ProjectBoards from '../components/ProjectBoards.vue'
import RelTime from '../components/RelTime.vue'
import StageBar from '../components/StageBar.vue'
import NotFound from './NotFound.vue'
import type { ProjectDetail } from '../types'

const props = defineProps<{ namespace: string; name: string }>()
const { data: p, error, notFound, loading } = useResource<ProjectDetail>(
  () => `/projects/${encodeURIComponent(props.namespace)}/${encodeURIComponent(props.name)}`,
)

const unhealthy = computed(() => (p.value?.conditions ?? []).filter((c) => c.status === 'False' && c.reason !== 'Suspended'))
</script>

<template>
  <NotFound v-if="notFound" />
  <div v-else class="page">
    <nav class="crumbs muted">
      <RouterLink :to="{ name: 'stream-project', params: { namespace, name } }">{{ name }}</RouterLink>
      <span>/</span>
      <span>Board</span>
    </nav>

    <div v-if="error" class="banner danger">{{ error }}</div>
    <div v-if="loading && !p" class="card empty">Loading…</div>

    <template v-if="p">
      <div class="row title">
        <h1>{{ p.name }}</h1>
        <Badge v-if="p.suspend" tone="warning">Suspended</Badge>
        <span class="spacer" />
        <ExtLink :href="p.gitlab.webURL" class="gl">{{ p.gitlab.project }}</ExtLink>
      </div>

      <EngineUsage v-for="u in p.engineUsage" :key="u.engine" :usage="u" class="engine-usage" />
      <div v-for="l in p.usageLimits" :key="l.engine" class="banner warning">
        <strong>{{ l.engine }}</strong>
        <span>hit its usage limit. Runs that need it wait; the limit resets <RelTime :time="l.until" />.</span>
        <span v-if="l.message" class="faint">{{ l.message }}</span>
      </div>
      <div v-for="c in unhealthy" :key="c.type" class="banner danger">
        <strong>{{ c.reason || c.type }}</strong>
        <span>{{ c.message }}</span>
      </div>
      <div v-if="p.suspend" class="banner info">
        The project is suspended: GitLab is not polled and no new runs start.
      </div>

      <div class="facts summary">
        <div class="fact">
          <div class="label">Tasks</div>
          <div class="value">{{ p.tasks }}</div>
        </div>
        <div class="fact">
          <div class="label">Parallel runs</div>
          <div class="value">up to {{ p.maxConcurrentRuns }}</div>
        </div>
        <div class="fact">
          <div class="label">Last sync</div>
          <div class="value"><RelTime :time="p.lastSyncTime" /></div>
        </div>
        <div class="fact">
          <div class="label">Poll interval</div>
          <div class="value">{{ p.pollInterval }}</div>
        </div>
        <div class="fact">
          <div class="label">Model spend</div>
          <div class="value">{{ cost(p.costUSD) }}</div>
        </div>
      </div>
      <div class="card stages">
        <StageBar :stages="p.stages" />
      </div>

      <ProjectBoards :namespace="namespace" :name="name" :project="p" />
    </template>
  </div>
</template>

<style scoped>
.crumbs {
  display: flex;
  gap: 8px;
  font-size: 13px;
  margin-bottom: 10px;
}

.title {
  gap: 12px;
}

.gl {
  font-size: 13px;
}

.engine-usage {
  margin-top: 6px;
}

.summary {
  margin-top: 18px;
}

.stages {
  margin-top: 12px;
  padding: 14px 16px;
}

.banner {
  margin-bottom: 12px;
}
</style>
