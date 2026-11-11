<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref } from 'vue'
import { useNow } from '../live'
import { pauseDuration, pausesLine, pausesSummary } from '../pauses'
import type { PauseHistoryView } from '../types'
import Truncated from '../ui/Truncated.vue'
import Markdown from './Markdown.vue'
import RelTime from './RelTime.vue'

// PausesCard is the milestone's pause history on the Pilot tab: the totals,
// the kinds by time paused, who resolved them and how, and the newest
// pauses. An open pause counts on up to now.
//
// Utilities only (docs/ui.md, "Pause cards"): a pause's reason is untrusted
// text, inline markdown on one line that opens in full on tap (Truncated),
// never a title. On phones a row's figures wrap below its kind and
// *Earlier pauses* is 44 px high.
const props = defineProps<{ pauses?: PauseHistoryView | null }>()
const now = useNow()
const s = computed(() => pausesSummary(props.pauses, now.value))
// The newest pauses show; the earlier ones of the view unfold on a click.
const newest = 5
const all = ref(false)
const rows = computed(() => (all.value ? s.value?.rows : s.value?.rows.slice(0, newest)) ?? [])
</script>

<template>
  <section v-if="s" class="min-w-0 text-[13px]" data-pauses>
    <h3 class="m-0 mb-2 flex justify-between text-[11.5px] font-semibold tracking-[0.06em] text-(--text-3) uppercase">
      <span>Pauses</span>
      <span>{{ s.count }}</span>
    </h3>
    <div>{{ pausesLine(s) }}</div>
    <div class="mt-2 mb-1.5 flex flex-wrap gap-1.5">
      <span v-for="k in s.kinds" :key="k.kind" class="rounded-full bg-(--surface-2) px-2 py-0.5 text-xs">
        {{ k.kind }} <b>{{ k.count }}</b> · {{ pauseDuration(k.sec) }}
      </span>
    </div>
    <div v-if="s.resolvers.length" class="faint text-xs">
      resolved by
      <template v-for="(r, i) in s.resolvers" :key="r.resolver">{{ i ? ', ' : ' ' }}{{ r.label }} {{ r.count }}</template>
    </div>
    <div v-if="s.vias.length" class="faint text-xs">
      ended via
      <template v-for="(v, i) in s.vias" :key="v.via">{{ i ? ', ' : ' ' }}{{ v.label }} {{ v.count }}</template>
    </div>
    <ul class="m-0 mt-2 grid list-none gap-1.5 p-0">
      <li
        v-for="(p, i) in rows"
        :key="i"
        class="min-w-0 rounded-lg px-2 py-1.5"
        :class="p.open ? 'bg-(--tone-warning-bg)' : 'bg-(--surface-2)'"
        data-pause
      >
        <div class="flex min-w-0 flex-wrap items-baseline gap-x-2">
          <b>{{ p.kind }}</b>
          <span v-if="p.item">{{ p.item }}</span>
          <span class="ms-auto">{{ p.duration }}</span>
          <span class="text-(--text-2)">{{ p.resolver }}<template v-if="p.via"> · {{ p.via }}</template></span>
        </div>
        <Truncated v-if="p.reason" class="text-xs text-(--text-2)">
          <Markdown mode="inline" :text="p.reason" />
        </Truncated>
        <small class="faint"><RelTime :time="p.since" /></small>
      </li>
    </ul>
    <UButton
      v-if="!all && s.rows.length > newest"
      color="neutral"
      variant="link"
      size="sm"
      class="mt-1.5 min-h-11 p-0 text-[12.5px] text-(--accent) tablet:min-h-0"
      :label="`Earlier pauses (${s.rows.length - newest})`"
      @click="all = true"
    />
  </section>
</template>
