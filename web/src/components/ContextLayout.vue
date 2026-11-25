<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref } from 'vue'
import { useBackClose } from '../ui/useBackClose'

// ContextLayout is the one layout of a page with a context panel
// (docs/contributing/web-ui.md, "Context panel and pause history"). From
// 1180 px the panel slot is a column beside the default slot; below, the
// content has the full width and the panel opens as a sheet from the right,
// closed by default. The page's own buttons (Context on a project; Issues,
// Queue and Decisions on a milestone) open it through v-model:open; the
// default slot's `sheet` says whether the panel is a sheet at this width, so
// the page shows the buttons only then. Back and Escape close the sheet
// (useBackClose), and focus returns to opener, the button that opened it. A
// query change inside the sheet keeps it open. opener false: a link in the
// sheet was followed, focus stays where the link led (no jump back to the
// header on phones, where the window scrolls).
//
// column: false keeps the panel a sheet at every width, for content that
// needs the full width (a milestone's Issues table). Without a panel slot
// the content is all there is.
const props = withDefaults(defineProps<{ column?: boolean; title?: string; description?: string; opener?: HTMLElement | null | false }>(), {
  column: true,
  title: 'Context',
  description: undefined,
  opener: null,
})
const open = defineModel<boolean>('open', { default: false })

// The sheet is loaded only where the panel is one, with its buttons.
const USlideover = defineAsyncComponent(() => import('@nuxt/ui/components/Slideover.vue'))

const narrowQuery = window.matchMedia('(max-width: 1179.98px)')
const narrow = ref(narrowQuery.matches)
// sheet: the panel is a sheet, not a column, at this width.
const sheet = computed(() => narrow.value || !props.column)
useBackClose(open, { keepOnQuery: true })
function focusOpener(e: Event) {
  if (props.opener === false) return void e.preventDefault()
  if (!props.opener?.isConnected) return
  e.preventDefault()
  props.opener.focus()
}
function onWidth() {
  narrow.value = narrowQuery.matches
  if (!sheet.value) open.value = false
}
onMounted(() => narrowQuery.addEventListener('change', onWidth))
onBeforeUnmount(() => narrowQuery.removeEventListener('change', onWidth))
</script>

<template>
  <div
    class="grid h-full min-h-dvh min-w-0 grid-cols-[minmax(0,1fr)]"
    :class="$slots.panel && !sheet && 'desktop:grid-cols-[minmax(0,1fr)_320px]'"
    data-context-layout
  >
    <slot :sheet="!!$slots.panel && sheet" />
    <slot v-if="$slots.panel && !sheet" name="panel" :sheet="false" />

    <!-- Below 1180 px, or without a column: the panel as a sheet from the
         right (docs/contributing/web-ui.md, "Sheets"). -->
    <USlideover
      v-if="$slots.panel && sheet"
      v-model:open="open"
      side="right"
      :title="title"
      :description="description ?? title"
      close-icon="i-lucide-x"
      :content="{ onCloseAutoFocus: focusOpener }"
      :ui="{
        overlay: 'z-40',
        content:
          'z-40 w-[min(360px,88vw)] pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)] pr-[env(safe-area-inset-right)]',
        header: 'min-h-0 gap-2 py-1 ps-4 pe-1 sm:ps-4 sm:pe-1',
        title: 'text-[15px] font-bold',
        description: 'sr-only',
        close: 'static ms-auto',
        body: 'min-h-0 min-w-0 p-0 sm:p-0',
      }"
    >
      <template #body>
        <slot name="panel" :sheet="true" />
      </template>
    </USlideover>
  </div>
</template>
