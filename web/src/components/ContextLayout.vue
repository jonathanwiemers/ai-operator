<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UTooltip from '@nuxt/ui/components/Tooltip.vue'
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useSidebarCollapsed } from '../sidebarRail'
import { useBackClose } from '../ui/useBackClose'

// ContextLayout is the one layout of a page with a context panel
// (docs/contributing/web-ui.md, "Context panel and pause history"). From
// 1180 px the panel slot is a column beside the default slot; below, the
// content has the full width and the panel opens as a sheet from the right,
// closed by default. The page's Now strip (the one-line summary under the
// header) opens it through v-model:open; the
// default slot's `sheet` says whether the panel is a sheet at this width, so
// the page shows the strip only then. Back and Escape close the sheet
// (useBackClose), and focus returns to opener, the strip that opened it. A
// query change inside the sheet keeps it open. opener false: a link in the
// sheet was followed, focus stays where the link led (no jump back to the
// header on phones, where the window scrolls).
//
// column: false keeps the panel a sheet at every width, for content that
// needs the full width (a milestone's Issues table). Without a panel slot
// the content is all there is.
//
// collapsible (R8 of "Desk: the Cockpit newest first by issue"): the column
// has a collapse button and shrinks to a rail with the rail slot (the
// counts) and an expand button. The state is the browser's, the same on
// every page (sidebarRail.ts); after a toggle, focus moves to the button
// that undoes it. Sheets have no rail.
const props = withDefaults(
  defineProps<{ column?: boolean; title?: string; description?: string; opener?: HTMLElement | null | false; collapsible?: boolean }>(),
  {
    column: true,
    title: 'Context',
    description: undefined,
    opener: null,
    collapsible: false,
  },
)
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
const collapsedState = useSidebarCollapsed()
const rail = computed(() => props.collapsible && collapsedState.value)
const root = ref<HTMLElement | null>(null)
async function setCollapsed(v: boolean) {
  collapsedState.value = v
  await nextTick()
  root.value?.querySelector<HTMLElement>(v ? '[data-rail-expand]' : '[data-rail-collapse]')?.focus()
}
onMounted(() => narrowQuery.addEventListener('change', onWidth))
onBeforeUnmount(() => narrowQuery.removeEventListener('change', onWidth))
</script>

<template>
  <div
    ref="root"
    class="grid h-full min-h-dvh min-w-0 grid-cols-[minmax(0,1fr)]"
    :class="$slots.panel && !sheet && (rail ? 'railed' : 'desktop:grid-cols-[minmax(0,1fr)_320px]')"
    data-context-layout
  >
    <slot :sheet="!!$slots.panel && sheet" />
    <template v-if="$slots.panel && !sheet">
      <!-- The rail: the expand button, then the counts. -->
      <div v-if="rail" class="rail" data-sidebar-rail>
        <UTooltip :text="`Expand ${title}`">
          <UButton
            color="neutral"
            variant="ghost"
            size="sm"
            icon="i-lucide-chevron-left"
            :aria-label="`Expand ${title}`"
            data-rail-expand
            @click="setCollapsed(false)"
          />
        </UTooltip>
        <slot name="rail" />
      </div>
      <!-- The column with its collapse button over the panel's top end. -->
      <div v-else-if="collapsible" class="collapsible">
        <slot name="panel" :sheet="false" />
        <UTooltip :text="`Collapse ${title}`">
          <UButton
            color="neutral"
            variant="ghost"
            size="sm"
            icon="i-lucide-chevron-right"
            class="collapse-btn"
            :aria-label="`Collapse ${title}`"
            data-rail-collapse
            @click="setCollapsed(true)"
          />
        </UTooltip>
      </div>
      <slot v-else name="panel" :sheet="false" />
    </template>

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

<!-- Scoped, not Tailwind utilities: they load with the page, not with the
     initial stylesheet (docs/contributing/web-ui.md, "Bundle budget"). -->
<style scoped>
.railed {
  grid-template-columns: minmax(0, 1fr) 52px;
}
.rail {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  min-width: 0;
  padding: 12px 0;
  border-left: 1px solid var(--border);
  background: var(--surface);
}
.collapsible {
  position: relative;
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  min-width: 0;
  min-height: 0;
}
.collapsible :deep(.collapse-btn) {
  position: absolute;
  top: 10px;
  inset-inline-end: 8px;
}
</style>
