<script setup lang="ts">
import { safeURL } from '../../format'
import ExtLink from '../ExtLink.vue'

// A list of links, each with its host.
defineProps<{ links: { title: string; url: string }[] }>()

const host = (u: string) => {
  const s = safeURL(u)
  return s ? new URL(s).host : ''
}
</script>

<template>
  <ul class="links">
    <li v-for="(l, i) in links" :key="i">
      <ExtLink :href="l.url" nofollow>{{ l.title }}</ExtLink>
      <span class="host faint">{{ host(l.url) }}</span>
    </li>
  </ul>
</template>

<style scoped>
.links {
  margin: 0;
  padding-left: 20px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.links li {
  overflow-wrap: anywhere;
}

.host {
  margin-left: 8px;
  font-size: 12px;
}
</style>
