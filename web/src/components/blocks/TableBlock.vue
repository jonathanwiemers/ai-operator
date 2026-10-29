<script setup lang="ts">
// A table of strings.
defineProps<{ columns: string[]; rows: string[][] }>()
</script>

<template>
  <div class="card wrap">
    <table class="list">
      <thead>
        <tr>
          <th v-for="(c, i) in columns" :key="i">{{ c }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(r, i) in rows" :key="i">
          <td v-for="(cell, j) in r" :key="j">{{ cell }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.wrap {
  overflow-x: auto;
}

td {
  overflow-wrap: anywhere;
  min-width: 80px;
}
</style>
