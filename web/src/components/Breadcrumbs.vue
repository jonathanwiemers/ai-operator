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

// Every crumb after the first, the fold among them, starts with a separator.
const item = 'min-w-0 items-center gap-1'
const sep = "before:text-(--text-3) before:content-['›']"
const mid = (i: number) => i > 0 && i < crumbs.value.length - 1
watch(
  () => route.fullPath,
  () => {
    open.value = false
  },
)
</script>

<template>
  <nav v-if="crumbs.length" aria-label="Breadcrumbs">
    <ol class="m-0 flex min-w-0 list-none flex-wrap items-center gap-1 p-0 text-[13px] text-(--text-3)">
      <template v-for="(c, i) in crumbs" :key="i">
        <!-- On phones the middle crumbs fold behind "…" until it is tapped. -->
        <li v-if="i === 1 && crumbs.length > 2" :class="[item, sep, open ? 'hidden' : 'hidden max-tablet:flex']">
          <button
            type="button"
            class="cursor-pointer rounded-(--radius-sm) border border-(--border) bg-(--surface-2) px-1.5 leading-[18px] text-(--text-2)"
            aria-label="Show all breadcrumbs"
            @click="open = true"
          >
            …
          </button>
        </li>
        <li :class="[item, i > 0 && sep, !open && mid(i) ? 'flex max-tablet:hidden' : 'flex']">
          <RouterLink v-if="c.to" :to="c.to" class="max-w-[28ch] truncate text-(--text-2)">{{ c.label }}</RouterLink>
          <span v-else aria-current="page" class="max-w-[28ch] truncate font-[550] text-(--text)">{{ c.label }}</span>
        </li>
      </template>
    </ol>
  </nav>
</template>
