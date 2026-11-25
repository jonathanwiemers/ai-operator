<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import { computed, ref } from 'vue'
import type { CockpitExchange } from '../../stream'
import EventCard from '../stream/EventCard.vue'

// CockpitConversation is the conversation with the pilot under the
// composer (R3 of "Desk: the Cockpit newest first by issue"): the latest
// exchange (the owner's message, then the pilot's answers), and Show
// earlier, which reveals the older exchanges 5 at a time, newest first.
// exchanges come from cockpitThreads' conversation; nothing when empty.
const props = defineProps<{ exchanges: CockpitExchange[] }>()

const step = 5
const count = ref(1)
const shown = computed(() => props.exchanges.slice(0, count.value))
const earlier = computed(() => props.exchanges.length - shown.value.length)
</script>

<template>
  <section v-if="exchanges.length" aria-label="Conversation with the pilot" class="conversation" data-cockpit-conversation>
    <ol class="exchanges">
      <li v-for="x in shown" :key="x.key" data-cockpit-exchange>
        <ol class="exchange">
          <li v-if="x.message"><EventCard :event="x.message" /></li>
          <li v-for="a in x.answers" :key="`${a.kind}:${a.id}`"><EventCard :event="a" /></li>
        </ol>
      </li>
    </ol>
    <UButton
      v-if="earlier"
      color="neutral"
      variant="ghost"
      size="sm"
      class="justify-self-start"
      icon="i-lucide-chevrons-down"
      :label="`Show earlier (${earlier})`"
      data-cockpit-earlier
      @click="count += step"
    />
  </section>
</template>

<style scoped>
.conversation {
  display: grid;
  gap: 8px;
}
.exchanges,
.exchange {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.exchanges {
  gap: 16px;
}
</style>
