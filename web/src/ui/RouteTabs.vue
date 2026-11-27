<script setup lang="ts" generic="T extends string">
// A row of underline tabs that are links (docs/contributing/web-ui.md,
// "RouteTabs"): UNavigationMenu, horizontal, with the highlight under the
// active tab. Each tab is a link, so the tab lives in the URL (?tab=,
// streamTabs.ts) and can be shared, reloaded and opened in a new tab;
// Nuxt UI's UTabs switches local state with buttons. The row never widens
// the page: its list scrolls sideways inside itself, and the active tab is
// scrolled into view.
import UNavigationMenu from '@nuxt/ui/components/NavigationMenu.vue'
import type { NavigationMenuItem } from '@nuxt/ui'
import { computed, onBeforeUnmount, onMounted, useId, watch } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { badgeLook } from '../badgeTone'
import type { Tab } from '../streamTabs'

const props = defineProps<{
  tabs: readonly Tab<T>[]
  active: T
  to: (id: T) => RouteLocationRaw
  // A count after a tab's label, shown when above 0.
  badges?: Partial<Record<T, number>>
  label?: string
}>()

// All tabs link to the same path and differ only in ?tab=, and the router's
// active match ignores the query: every tab would be active. So each item
// says whether it is active, and only that one gets aria-current="page".
const warning = badgeLook.warning
const items = computed<NavigationMenuItem[]>(() =>
  props.tabs.map((t): NavigationMenuItem => {
    const count = props.badges?.[t.id] ?? 0
    return {
      label: t.label,
      to: props.to(t.id),
      active: t.id === props.active,
      // The warning tone of a Desk badge (badgeTone.ts).
      badge: count > 0 ? { label: String(count), ...warning } : undefined,
    }
  }),
)

// The menu renders several root nodes, so the nav is found by its id.
const id = useId()
function list(): HTMLElement | null {
  return document.getElementById(id)?.querySelector<HTMLElement>('[data-slot="list"]') ?? null
}

// reveal scrolls the list, and only the list (scrollIntoView would move
// the page too), so the active tab is inside it. It stops at a tab's start,
// where the list snaps, so snapping does not move the tab out again.
// Tab widths are fractional and offsetLeft rounds, so positions come from
// the boxes, and the end keeps a pixel to spare for the scroll's rounding.
function reveal(): void {
  const ul = list()
  if (!ul) return
  const el = ul.querySelector<HTMLElement>('a[aria-current="page"]')?.closest('li')
  if (!el) return
  const origin = ul.getBoundingClientRect().left + ul.clientLeft - ul.scrollLeft
  const startOf = (li: Element) => li.getBoundingClientRect().left - origin
  const start = startOf(el)
  const end = start + el.getBoundingClientRect().width
  if (start < ul.scrollLeft) {
    ul.scrollLeft = Math.floor(start)
  } else if (end > ul.scrollLeft + ul.clientWidth) {
    const min = end - ul.clientWidth + 1
    const tab = [...ul.querySelectorAll(':scope > li')].map(startOf).find((s) => s >= min)
    ul.scrollLeft = tab === undefined ? Math.ceil(min) : Math.floor(tab)
  }
}

// The list's width changes when the header around it loads or the phone
// turns; the active tab stays in view.
let resize: ResizeObserver | undefined
onMounted(() => {
  reveal()
  const ul = list()
  if (typeof ResizeObserver !== 'undefined' && ul) {
    resize = new ResizeObserver(() => reveal())
    resize.observe(ul)
  }
})
onBeforeUnmount(() => resize?.disconnect())
watch(() => [props.active, props.tabs], reveal, { flush: 'post' })
</script>

<template>
  <!-- No item has children, so the menu opens no viewport or popover. The
       list is the scroller; its end padding lets the last tab scroll fully
       into view whatever the rounding of the widest scroll. Tabs are 44 px
       high on phones (style.css, "Hit areas"). The focus
       outline sits on the link's inset ::before, inside the list's clip.
       data-route-tabs: a swipe that starts on the strip is no tab swipe
       (SwipeTabs). -->
  <UNavigationMenu
    :id="id"
    data-route-tabs
    :aria-label="label ?? 'View'"
    orientation="horizontal"
    highlight
    color="neutral"
    :items="items"
    class="min-w-0 max-w-full border-b border-(--border)"
    :ui="{
      list: 'snap-x snap-proximity overflow-x-auto pe-0.5',
      item: 'shrink-0 snap-start py-0',
      link: 'min-h-11 whitespace-nowrap before:inset-1 after:bottom-0 hover:no-underline tablet:min-h-0',
      viewportWrapper: 'hidden',
    }"
  />
</template>
