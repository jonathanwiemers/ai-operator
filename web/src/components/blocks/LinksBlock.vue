<script setup lang="ts">
import { safeURL } from '../../format'
import ExtLink from '../ExtLink.vue'

// A list of links, each with its host. On a phone the host goes below its
// link and each link is at least 44 px high.
defineProps<{ links: { title: string; url: string }[] }>()

const host = (u: string) => {
  const s = safeURL(u)
  return s ? new URL(s).host : ''
}
</script>

<template>
  <ul class="m-0 flex flex-col gap-1 ps-5" data-block="links">
    <li v-for="(l, i) in links" :key="i" class="[overflow-wrap:anywhere]">
      <ExtLink :href="l.url" nofollow class="min-h-11 tablet:min-h-0">{{ l.title }}</ExtLink>
      <span v-if="host(l.url)" class="block text-xs text-(--text-3) tablet:ms-2 tablet:inline">{{ host(l.url) }}</span>
    </li>
  </ul>
</template>
