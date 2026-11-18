<script setup lang="ts">
import { computed } from 'vue'
import type { Facts } from '../generated/problem'
import { useNow } from '../live'
import { factRows } from '../problemFacts'
import ExtLink from './ExtLink.vue'

// ProblemFacts is the facts strip of a problem card (R8): one row per fact
// the operator collected (problemFacts.ts), parts separated by "·". Agent
// text is plain text; rows wrap anywhere, so nothing overflows at 375 px.
const props = defineProps<{ namespace: string; name: string; facts: Facts }>()
const now = useNow()
const rows = computed(() => factRows(props.facts, { namespace: props.namespace, name: props.name }, now.value))
</script>

<template>
  <ul class="grid min-w-0 gap-1 text-[12.5px] text-(--text-2)" data-problem-facts>
    <li v-for="r in rows" :key="r.key" class="min-w-0 wrap-anywhere" :data-fact="r.key">
      <template v-for="(p, i) in r.parts" :key="i">
        <span v-if="i" aria-hidden="true" class="text-(--text-3)"> · </span>
        <RouterLink v-if="p.to" :to="p.to">{{ p.text }}</RouterLink>
        <ExtLink v-else-if="p.href" :href="p.href">{{ p.text }}</ExtLink>
        <span v-else>{{ p.text }}</span>
      </template>
    </li>
  </ul>
</template>
