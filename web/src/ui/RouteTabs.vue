<script setup lang="ts" generic="T extends string">
// A tab strip of links (docs/ui.md, "RouteTabs"): each tab is a RouterLink,
// so the tab lives in the URL (?tab=, streamTabs.ts) and can be shared,
// reloaded and opened in a new tab. Nuxt UI's UTabs switches local state
// with buttons. The strip never widens the page: it scrolls sideways inside
// itself, and the active tab is scrolled into view.
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import type { Tab } from '../streamTabs'

const props = defineProps<{
  tabs: readonly Tab<T>[]
  active: T
  to: (id: T) => RouteLocationRaw
  // A count after a tab's label, shown when above 0.
  badges?: Partial<Record<T, number>>
  label?: string
}>()

const strip = ref<HTMLElement | null>(null)

// reveal scrolls the strip, and only the strip (scrollIntoView would move
// the page too), so the active tab is inside it. It stops at a tab's start,
// where the strip snaps, so snapping does not move the tab out again.
function reveal(): void {
  const nav = strip.value
  if (!nav) return
  const links = [...nav.querySelectorAll<HTMLElement>(':scope > a')]
  const el = links.find((a) => a.getAttribute('aria-current') === 'page')
  if (!el) return
  const start = el.offsetLeft
  const end = start + el.offsetWidth
  if (start < nav.scrollLeft) {
    nav.scrollLeft = start
  } else if (end > nav.scrollLeft + nav.clientWidth) {
    const min = end - nav.clientWidth
    nav.scrollLeft = links.find((a) => a.offsetLeft >= min)?.offsetLeft ?? min
  }
}

// The strip's width changes when the header around it loads or the phone
// turns; the active tab stays in view.
let resize: ResizeObserver | undefined
onMounted(() => {
  reveal()
  if (typeof ResizeObserver !== 'undefined' && strip.value) {
    resize = new ResizeObserver(() => reveal())
    resize.observe(strip.value)
  }
})
onBeforeUnmount(() => resize?.disconnect())
watch(() => [props.active, props.tabs], reveal, { flush: 'post' })
</script>

<template>
  <nav
    ref="strip"
    :aria-label="label ?? 'View'"
    class="relative flex min-w-0 max-w-full snap-x snap-proximity gap-0.5 overflow-x-auto rounded-(--radius-sm) bg-(--surface-3) p-0.5"
  >
    <RouterLink
      v-for="t in tabs"
      :key="t.id"
      :to="to(t.id)"
      :aria-current="active === t.id ? 'page' : undefined"
      class="inline-flex min-h-11 shrink-0 snap-start items-center gap-1.5 rounded-[5px] px-3 py-[3px] text-[13px] font-medium whitespace-nowrap hover:no-underline tablet:min-h-0"
      :class="active === t.id ? 'bg-(--surface) text-(--text) shadow-(--shadow)' : 'text-(--text-2) hover:text-(--text)'"
    >
      {{ t.label }}
      <span
        v-if="(badges?.[t.id] ?? 0) > 0"
        class="rounded-full bg-(--tone-warning-bg) px-1.5 text-[11px] leading-4 font-semibold text-(--tone-warning-fg)"
      >
        {{ badges?.[t.id] }}
      </span>
    </RouterLink>
  </nav>
</template>
