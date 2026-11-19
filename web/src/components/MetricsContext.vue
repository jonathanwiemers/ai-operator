<script setup lang="ts">
// Context (docs/contributing/web-ui.md, "Metrics page"): per project, how big
// a fresh context is before the agent starts (the median first turn of the
// calls that started their session, per role), and the five tools whose
// results put the most into the context over the range, in tokens estimated
// from their bytes.
import { tokenCount } from '../format'
import type { ProjectContext, RoleMedian, ToolUsage } from '../generated/views'
import { byteLabel, hasContext } from '../metrics'
import DataList, { type DataListColumn } from '../ui/DataList.vue'

defineProps<{ context: ProjectContext[] }>()

const baseColumns: DataListColumn[] = [
  { key: 'role', label: 'Role' },
  { key: 'median', label: 'Median base context', numeric: true },
  { key: 'calls', label: 'Fresh calls', numeric: true },
]
const toolColumns: DataListColumn[] = [
  { key: 'tool', label: 'Tool' },
  { key: 'tokens', label: 'Result tokens', numeric: true },
  { key: 'bytes', label: 'Size', numeric: true },
  { key: 'calls', label: 'Results', numeric: true },
]
const roleKey = (r: RoleMedian) => r.role
const toolKey = (t: ToolUsage) => t.tool
</script>

<template>
  <section class="section" aria-labelledby="costs-context" data-costs-context>
    <div class="section-head flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="costs-context">Context</h2>
      <span class="faint">What a fresh session starts with, and the tools that fill it most</span>
    </div>
    <p v-if="!hasContext(context)" class="card empty m-0" data-context-empty>No context recorded in this range.</p>
    <div v-else class="grid min-w-0 gap-3">
      <div v-for="p in context" :key="p.project" class="card min-w-0 overflow-hidden" :data-context-project="p.project">
        <h3 class="m-0 px-3.5 pt-3 text-[13px] font-semibold [overflow-wrap:anywhere]">{{ p.project }}</h3>
        <div class="grid min-w-0 desktop:grid-cols-2">
          <div class="min-w-0" data-context-base>
            <h4 class="m-0 px-3.5 pt-2 text-xs font-semibold text-(--text-3)">Base context by role</h4>
            <p v-if="!p.baseContext.length" class="faint m-0 px-3.5 py-3">No fresh session in this range.</p>
            <DataList v-else :columns="baseColumns" :rows="p.baseContext" :row-key="roleKey">
              <template #cell-median="{ row }">{{ tokenCount(row.medianTokens) }} tokens</template>
            </DataList>
          </div>
          <div class="min-w-0" data-context-tools>
            <h4 class="m-0 px-3.5 pt-2 text-xs font-semibold text-(--text-3)">Top tools by result tokens</h4>
            <p v-if="!p.topTools.length" class="faint m-0 px-3.5 py-3">No tool results in this range.</p>
            <DataList v-else :columns="toolColumns" :rows="p.topTools" :row-key="toolKey">
              <template #cell-tool="{ row }"><span class="[overflow-wrap:anywhere]">{{ row.tool }}</span></template>
              <template #cell-tokens="{ row }">~{{ tokenCount(row.tokens) }}</template>
              <template #cell-bytes="{ row }">{{ byteLabel(row.bytes) }}</template>
            </DataList>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
