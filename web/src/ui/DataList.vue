<script setup lang="ts" generic="T extends object">
// One table that stacks on phones (docs/ui.md, "DataList"): below 760 px each
// row is a card and each cell a label and value pair; from the tablet width up
// it is a table. With `scroll` it stays a table at every width and scrolls
// inside its own container, never the page.
import { computed } from 'vue'

export interface DataListColumn {
  key: string
  label: string
  // Classes for the column's cells.
  class?: string
  // numeric: right-aligned in the table, with tabular figures.
  numeric?: boolean
}

const props = defineProps<{
  columns: DataListColumn[]
  rows: T[]
  rowKey: (row: T) => string
  scroll?: boolean
  // expanded: the rows that show the `detail` slot in a full-width row below.
  expanded?: (row: T) => boolean
}>()

defineSlots<Record<string, (props: { row: T }) => unknown>>()

const stack = computed(() => !props.scroll)

function value(row: T, key: string): unknown {
  return (row as Record<string, unknown>)[key]
}
</script>

<template>
  <div class="overflow-x-auto">
    <table
      class="w-full border-collapse text-left"
      :class="stack ? 'block tablet:table' : 'table min-w-max'"
    >
      <thead :class="stack ? 'hidden tablet:table-header-group' : ''">
        <tr>
          <th
            v-for="c in columns"
            :key="c.key"
            scope="col"
            class="border-b border-(--border) bg-(--surface-2) px-3.5 py-2 text-xs font-semibold text-(--text-3)"
            :class="c.numeric ? 'text-right' : 'text-left'"
          >
            {{ c.label }}
          </th>
        </tr>
      </thead>
      <tbody :class="stack ? 'block tablet:table-row-group' : ''">
        <template v-for="row in rows" :key="rowKey(row)">
          <tr
            class="relative border-b border-(--border) last:border-b-0"
            :class="stack ? 'block px-3.5 py-2 tablet:table-row tablet:p-0' : ''"
          >
            <td
              v-for="c in columns"
              :key="c.key"
              class="align-top"
              :class="[
                stack
                  ? 'grid min-w-0 grid-cols-[6.5rem_minmax(0,1fr)] items-baseline gap-2 py-1 tablet:table-cell tablet:px-3.5 tablet:py-2.5'
                  : 'px-3.5 py-2.5',
                c.numeric && (stack ? 'tabular-nums tablet:text-right' : 'tabular-nums text-right'),
                c.class,
              ]"
            >
              <span v-if="stack" aria-hidden="true" data-label class="text-xs font-semibold text-(--text-3) tablet:hidden">{{ c.label }}</span>
              <span class="min-w-0 break-words">
                <slot :name="`cell-${c.key}`" :row="row">{{ value(row, c.key) }}</slot>
              </span>
            </td>
          </tr>
          <tr
            v-if="expanded?.(row)"
            class="border-b border-(--border) bg-(--surface-2) last:border-b-0"
            :class="stack ? 'block tablet:table-row' : ''"
          >
            <td :colspan="columns.length" class="px-3.5 py-2" :class="stack ? 'block tablet:table-cell' : ''">
              <!-- w-0 min-w-full: the detail never widens the table, wide content scrolls inside it. -->
              <div class="w-0 min-w-full">
                <slot name="detail" :row="row" />
              </div>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>
