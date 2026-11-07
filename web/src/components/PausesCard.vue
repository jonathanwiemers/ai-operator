<script setup lang="ts">
import { computed, ref } from 'vue'
import { useNow } from '../live'
import { pauseTitle } from '../pauseCard'
import { pauseDuration, pausesLine, pausesSummary } from '../pauses'
import type { PauseHistoryView } from '../types'
import RelTime from './RelTime.vue'

// PausesCard is the milestone's pause history in the context panel: the
// totals, the kinds by time paused, who resolved them and how, and the
// newest pauses. An open pause counts on up to now. Reasons are untrusted text.
const props = defineProps<{ pauses?: PauseHistoryView | null }>()
const now = useNow()
const s = computed(() => pausesSummary(props.pauses, now.value))
// The newest pauses show; the earlier ones of the view unfold on a click.
const newest = 5
const all = ref(false)
const rows = computed(() => (all.value ? s.value?.rows : s.value?.rows.slice(0, newest)) ?? [])
</script>

<template>
  <div v-if="s" class="panel">
    <h3>
      <span>Pauses</span>
      <span>{{ s.count }}</span>
    </h3>
    <div class="line">{{ pausesLine(s) }}</div>
    <div class="chips">
      <span v-for="k in s.kinds" :key="k.kind" class="chip" :title="pauseTitle(k.kind)">
        {{ k.kind }} <b>{{ k.count }}</b> · {{ pauseDuration(k.sec) }}
      </span>
    </div>
    <div v-if="s.resolvers.length" class="faint by">
      resolved by
      <template v-for="(r, i) in s.resolvers" :key="r.resolver">{{ i ? ', ' : ' ' }}{{ r.label }} {{ r.count }}</template>
    </div>
    <div v-if="s.vias.length" class="faint by">
      ended via
      <template v-for="(v, i) in s.vias" :key="v.via">{{ i ? ', ' : ' ' }}{{ v.label }} {{ v.count }}</template>
    </div>
    <ul class="list">
      <li v-for="(p, i) in rows" :key="i" :class="{ open: p.open }">
        <div class="row">
          <b :title="pauseTitle(p.kind)">{{ p.kind }}</b>
          <span v-if="p.item">{{ p.item }}</span>
          <span class="spacer" />
          <span>{{ p.duration }}</span>
          <span class="who">{{ p.resolver }}<template v-if="p.via"> · {{ p.via }}</template></span>
        </div>
        <div v-if="p.reason" class="reason" :title="p.reason">{{ p.reason }}</div>
        <small class="faint"><RelTime :time="p.since" /></small>
      </li>
    </ul>
    <button v-if="!all && s.rows.length > newest" type="button" class="more" @click="all = true">
      Earlier pauses ({{ s.rows.length - newest }})
    </button>
  </div>
</template>

<style scoped>
.panel h3 {
  font-size: 11.5px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--text-3);
  margin: 0 0 8px;
  display: flex;
  justify-content: space-between;
  font-weight: 600;
}
.line {
  font-size: 13px;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 8px 0 6px;
}
.chip {
  font-size: 12px;
  background: var(--surface-2);
  border-radius: 999px;
  padding: 2px 8px;
}
.by {
  font-size: 12px;
}
.more {
  margin-top: 6px;
  font-size: 12.5px;
  background: none;
  border: 0;
  padding: 0;
  color: var(--accent);
  cursor: pointer;
}
.list {
  list-style: none;
  margin: 8px 0 0;
  padding: 0;
  display: grid;
  gap: 6px;
}
.list li {
  font-size: 13px;
  padding: 6px 8px;
  border-radius: 8px;
  background: var(--surface-2);
}
.list li.open {
  background: var(--tone-warning-bg);
}
.row {
  display: flex;
  gap: 8px;
  align-items: baseline;
}
.spacer {
  flex: 1;
}
.who {
  color: var(--text-2);
}
.reason {
  color: var(--text-2);
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
