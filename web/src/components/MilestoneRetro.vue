<script setup lang="ts">
import { computed } from 'vue'
import Badge from './Badge.vue'
import ExtLink from './ExtLink.vue'
import RelTime from './RelTime.vue'
import MarkdownBody from './blocks/MarkdownBlock.vue'
import { cost, tokenCount } from '../format'
import { driverShares, followUpStateLabel, followUpStateTone } from '../retrospective'
import type { RetrospectiveView } from '../types'

// A milestone's Retrospective tab (R12), read-only: when it was posted and
// its comment, the text, the cost drivers and the leftovers with what came
// of them. Text, titles and bodies are untrusted markdown: markdown.ts
// renders them to VNodes, so HTML in them shows as text.
const props = defineProps<{ retrospective?: RetrospectiveView }>()

const r = computed(() => props.retrospective)
const drivers = computed(() => r.value?.costDrivers ?? [])
const driverPct = computed(() => driverShares(drivers.value))
</script>

<template>
  <section class="mretro">
    <div v-if="!r" class="empty">The retrospective is written when the autopilot is done.</div>
    <template v-else>
      <div v-if="r.postedAt || r.noteURL" class="meta">
        <span v-if="r.postedAt">Posted <RelTime :time="r.postedAt" /></span>
        <ExtLink v-if="r.noteURL" :href="r.noteURL">Comment on GitLab</ExtLink>
      </div>
      <div v-if="r.failed" class="banner warning">No retrospective: {{ r.failed }}</div>

      <div v-if="r.text" class="block text"><MarkdownBody :text="r.text" /></div>
      <p v-else-if="!r.failed" class="note">The text of this retrospective is not stored.</p>

      <div v-if="drivers.length" class="block">
        <h3>Cost drivers</h3>
        <ul class="drivers">
          <li v-for="(d, k) in drivers" :key="d.kind + d.label">
            <span class="label">{{ d.label }}</span>
            <Badge tone="muted">{{ d.kind }}</Badge>
            <span class="num">{{ d.costKnown ? cost(d.costUSD) : 'n/a' }}</span>
            <span class="num">{{ tokenCount(d.tokens) }} tok</span>
            <span class="num pct">{{ driverPct[k] }}</span>
          </li>
        </ul>
      </div>

      <div v-if="r.followUps.length" class="block">
        <h3>Leftovers <span>{{ r.followUps.length }}</span></h3>
        <ul class="followups">
          <li v-for="f in r.followUps" :key="f.number">
            <div class="head">
              <span class="n">{{ f.number }}.</span>
              <span class="title">{{ f.title }}</span>
              <ExtLink v-if="f.targetURL" :href="f.targetURL" class="state">
                <Badge :tone="followUpStateTone(f.state)">{{ followUpStateLabel(f) }}</Badge>
              </ExtLink>
              <Badge v-else class="state" :tone="followUpStateTone(f.state)">{{ followUpStateLabel(f) }}</Badge>
            </div>
            <details v-if="f.body">
              <summary>Details</summary>
              <MarkdownBody :text="f.body" />
            </details>
          </li>
        </ul>
      </div>
    </template>
  </section>
</template>

<style scoped>
.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  font-size: 13px;
  color: var(--text-2);
}
.banner {
  margin-top: 12px;
}
.block {
  margin-top: 16px;
}
.text {
  padding: 14px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  font-size: 14px;
}
.note {
  margin: 12px 0 0;
  font-size: 13px;
  color: var(--text-3);
}
ul {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
}
li {
  padding: 8px 14px;
  border-top: 1px solid var(--border);
  font-size: 13px;
}
li:first-child {
  border-top: 0;
}
.drivers li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto 64px 72px 44px;
  align-items: center;
  gap: 10px;
}
.label,
.title {
  font-weight: 600;
  overflow-wrap: anywhere;
}
.num {
  font-variant-numeric: tabular-nums;
  color: var(--text-2);
  text-align: right;
  white-space: nowrap;
}
.pct {
  color: var(--text);
}
.followups .head {
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.n {
  color: var(--text-3);
  font-variant-numeric: tabular-nums;
}
.title {
  flex: 1;
  min-width: 0;
}
.state {
  flex: none;
}
details {
  margin: 6px 0 2px 22px;
}
summary {
  cursor: pointer;
  font-size: 12.5px;
  color: var(--text-3);
}
details[open] summary {
  margin-bottom: 6px;
}
.empty {
  padding: 32px;
  text-align: center;
  color: var(--text-3);
}
@media (max-width: 760px) {
  .drivers li {
    grid-template-columns: auto 1fr auto auto;
  }
  .drivers .label {
    grid-column: 1 / -1;
  }
  .drivers .num {
    text-align: left;
  }
  .followups .head {
    flex-wrap: wrap;
  }
}
</style>
