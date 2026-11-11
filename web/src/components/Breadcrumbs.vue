<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { crumbNames } from '../crumbNames'
import { crumbsFor } from '../crumbs'
import { isRouteName } from '../routeNames'

// The app's breadcrumbs (R19), built from the route. On narrow screens the
// middle crumbs fold behind "…"; the first and the last stay.
const route = useRoute()
const crumbs = computed(() =>
  isRouteName(route.name) ? crumbsFor({ name: route.name, params: route.params, query: route.query }, crumbNames) : [],
)
const open = ref(false)
watch(
  () => route.fullPath,
  () => {
    open.value = false
  },
)
</script>

<template>
  <nav v-if="crumbs.length" class="breadcrumbs" :class="{ open }" aria-label="Breadcrumbs">
    <ol>
      <template v-for="(c, i) in crumbs" :key="i">
        <li v-if="i === 1 && crumbs.length > 2" class="fold">
          <button type="button" aria-label="Show all breadcrumbs" @click="open = true">…</button>
        </li>
        <li :class="{ mid: i > 0 && i < crumbs.length - 1 }">
          <RouterLink v-if="c.to" :to="c.to">{{ c.label }}</RouterLink>
          <span v-else aria-current="page">{{ c.label }}</span>
        </li>
      </template>
    </ol>
  </nav>
</template>

<style scoped>
ol {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  list-style: none;
  margin: 0;
  padding: 0;
  font-size: 13px;
  color: var(--text-3);
  min-width: 0;
}
li {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
li + li::before {
  content: '›';
  color: var(--text-3);
}
li a,
li span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 28ch;
}
li a {
  color: var(--text-2);
}
li span[aria-current] {
  color: var(--text);
  font-weight: 550;
}
.fold {
  display: none;
}
.fold button {
  border: 1px solid var(--border);
  background: var(--surface-2);
  border-radius: var(--radius-sm);
  color: var(--text-2);
  padding: 0 6px;
  line-height: 18px;
  cursor: pointer;
}
@media (max-width: 759.98px) {
  .breadcrumbs:not(.open) .mid {
    display: none;
  }
  .breadcrumbs:not(.open) .fold {
    display: flex;
  }
}
</style>
