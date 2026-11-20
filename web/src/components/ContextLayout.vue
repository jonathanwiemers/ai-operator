<script setup lang="ts">
import { defineAsyncComponent, onBeforeUnmount, onMounted, ref } from 'vue'
import { useBackClose } from '../ui/useBackClose'

// ContextLayout is the one layout of a page with a context panel
// (docs/contributing/web-ui.md, "Context panel and pause history"). From
// 1180 px the panel slot is a column beside the default slot; below, the
// content has the full width and the panel opens as a sheet from the right,
// closed by default. The page's own Context button opens it through
// v-model:open; the default slot's `sheet` says whether the panel is a sheet
// at this width, so the page shows the button only then. Back closes the
// sheet (useBackClose).
//
// column: false keeps the panel a sheet at every width below 1180 px and
// shows none from 1180 px (a milestone page, whose tabs take the full width).
// Without a panel slot the content is all there is.
withDefaults(defineProps<{ column?: boolean; title?: string; description?: string }>(), {
  column: true,
  title: 'Context',
  description: undefined,
})
const open = defineModel<boolean>('open', { default: false })

// The sheet is loaded below 1180 px only, where the Context button is.
const USlideover = defineAsyncComponent(() => import('@nuxt/ui/components/Slideover.vue'))

const narrowQuery = window.matchMedia('(max-width: 1179.98px)')
const narrow = ref(narrowQuery.matches)
useBackClose(open)
function onWidth() {
  narrow.value = narrowQuery.matches
  if (!narrowQuery.matches) open.value = false
}
onMounted(() => narrowQuery.addEventListener('change', onWidth))
onBeforeUnmount(() => narrowQuery.removeEventListener('change', onWidth))
</script>

<template>
  <div
    class="grid h-full min-h-dvh min-w-0 grid-cols-[minmax(0,1fr)]"
    :class="$slots.panel && column && 'desktop:grid-cols-[minmax(0,1fr)_320px]'"
    data-context-layout
  >
    <slot :sheet="!!$slots.panel && narrow" />
    <slot v-if="$slots.panel && column && !narrow" name="panel" :sheet="false" />

    <!-- Below 1180 px: the panel as a sheet from the right
         (docs/contributing/web-ui.md, "Sheets"). -->
    <USlideover
      v-if="$slots.panel && narrow"
      v-model:open="open"
      side="right"
      :title="title"
      :description="description ?? title"
      close-icon="i-lucide-x"
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
