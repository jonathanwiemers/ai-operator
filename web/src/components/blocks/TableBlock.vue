<script setup lang="ts">
// A table of strings. It keeps its columns at every width: each column is at
// least 8rem wide, so on a phone the table scrolls inside its own container,
// never the page. Long cells wrap.
defineProps<{ columns: string[]; rows: string[][] }>()
</script>

<template>
  <div class="card overflow-x-auto" data-block="table">
    <table class="w-full border-collapse text-left">
      <thead>
        <tr>
          <th
            v-for="(c, i) in columns"
            :key="i"
            scope="col"
            class="min-w-32 border-b border-(--border) bg-(--surface-2) px-3.5 py-2 text-xs font-semibold text-(--text-3)"
          >
            {{ c }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in rows" :key="i" class="border-b border-(--border) last:border-b-0">
          <td v-for="(cell, j) in r" :key="j" class="min-w-32 px-3.5 py-2.5 align-top [overflow-wrap:anywhere]">{{ cell }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
