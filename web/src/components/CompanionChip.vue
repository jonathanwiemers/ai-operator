<script setup lang="ts">
import { computed } from 'vue'
import { companionLabel, companionTitle, companionTone } from '../companion'
import type { CompanionView } from '../types'
import Badge from './Badge.vue'
import ExtLink from './ExtLink.vue'

// CompanionChip is an issue's companion merge request: its landing chip,
// with the reason as tooltip, linking to the merge request. plain renders
// it without the link, for a card that is a link itself.
const props = defineProps<{ companion: CompanionView; plain?: boolean }>()

const tone = computed(() => companionTone(props.companion))
const label = computed(() => companionLabel(props.companion))
const title = computed(() => companionTitle(props.companion))
</script>

<template>
  <ExtLink v-if="!plain && companion.mrURL" :href="companion.mrURL" class="hover:no-underline" :title="title">
    <Badge :tone="tone" :title="title">companion: {{ label }}</Badge>
  </ExtLink>
  <Badge v-else :tone="tone" :title="title">companion: {{ label }}</Badge>
</template>
