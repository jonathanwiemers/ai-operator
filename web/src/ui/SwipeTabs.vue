<script setup lang="ts" generic="T extends string">
// The swipe surface of a RouteTabs row (docs/contributing/web-ui.md,
// "SwipeTabs"): on a touch screen, the content it wraps follows a sideways
// finger, and letting go far or fast enough opens the next or previous
// tab, as a tap does (router.push). Touch only; the numbers are in
// tabSwipe.ts. The neighbour is a placeholder: no tab content mounts.
import USkeleton from '@nuxt/ui/components/Skeleton.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter, type RouteLocationRaw } from 'vue-router'
import type { Tab } from '../streamTabs'
import { dragOffset, lockAxis, neighbour, releaseSpeed, settle, type Axis, type Sample } from '../tabSwipe'

const props = defineProps<{
  tabs: readonly Tab<T>[]
  active: T
  to: (id: T) => RouteLocationRaw
  // Ignore every pointer, for a row that is not shown at this width.
  disabled?: boolean
}>()

const router = useRouter()
const root = ref<HTMLElement>()
const track = ref<HTMLElement>()

// Where the neighbour panel sits while dragging toward an existing tab.
const side = ref<'next' | 'prev' | null>(null)
// A sideways drag or its finishing slide runs: only then is the root
// clipped and the track positioned.
const moving = ref(false)
const index = computed(() => props.tabs.findIndex((t) => t.id === props.active))
const panelLabel = computed(() => {
  if (side.value == null) return ''
  return props.tabs[index.value + (side.value === 'next' ? 1 : -1)]?.label ?? ''
})

const edge = 24 // px from the viewport's sides, where iOS has its back gesture
const settleMs = 200
// The placeholder's skeleton lines, by width.
const lines = ['75%', '100%', '83%', '66%']

interface Drag {
  id: number
  x: number
  y: number
  width: number
  index: number
  count: number
  axis: Axis
  dx: number
  samples: Sample[]
}
let drag: Drag | null = null
let settling = false // the finishing slide or the spring-back runs
let frame = 0
let timer: ReturnType<typeof setTimeout> | undefined
let swallow = false
let swallowTimer: ReturnType<typeof setTimeout> | undefined

// A touch that starts here is not a tab swipe: a sideways scroller (it
// scrolls itself; one that is pan-y, as the tab's own scroller, does not
// pan sideways by touch), a text field, a tab strip, the screen edge or
// while text is selected.
function excluded(e: PointerEvent): boolean {
  if (e.clientX < edge || e.clientX > window.innerWidth - edge) return true
  if ((window.getSelection()?.toString() ?? '') !== '') return true
  const stop = root.value
  for (let el = e.target instanceof Element ? e.target : null; el && el !== stop; el = el.parentElement) {
    if (el.matches('input, textarea, select, [data-route-tabs], [contenteditable]:not([contenteditable="false"])')) return true
    if (el.scrollWidth > el.clientWidth) {
      const style = getComputedStyle(el)
      const x = style.overflowX
      if ((x === 'auto' || x === 'scroll') && style.touchAction !== 'pan-y') return true
    }
  }
  return false
}

function onDown(e: PointerEvent): void {
  swallow = false
  clearTimeout(swallowTimer)
  if (props.disabled || e.pointerType !== 'touch' || !e.isPrimary || drag || settling) return
  if (index.value < 0 || excluded(e)) return
  drag = {
    id: e.pointerId,
    x: e.clientX,
    y: e.clientY,
    width: root.value?.getBoundingClientRect().width ?? window.innerWidth,
    index: index.value,
    count: props.tabs.length,
    axis: 'pending',
    dx: 0,
    samples: [{ x: e.clientX, t: e.timeStamp }],
  }
}

function paint(): void {
  frame = 0
  if (!drag || !track.value) return
  track.value.style.transform = `translateX(${dragOffset(drag.dx, drag.index, drag.count)}px)`
}

function onMove(e: PointerEvent): void {
  if (!drag || e.pointerId !== drag.id) return
  const dx = e.clientX - drag.x
  if (drag.axis === 'pending') {
    drag.axis = lockAxis(dx, e.clientY - drag.y)
    if (drag.axis === 'y') {
      drag = null // a scroll for the rest of this touch
      return
    }
    if (drag.axis === 'pending') return
    root.value?.setPointerCapture(e.pointerId)
    moving.value = true
    // The click after the drag is swallowed; the flag clears itself
    // however the drag ends (pointerup, pointercancel).
    swallow = true
    clearTimeout(swallowTimer)
    swallowTimer = setTimeout(() => (swallow = false), 1000)
  }
  drag.dx = dx
  drag.samples.push({ x: e.clientX, t: e.timeStamp })
  if (drag.samples.length > 32) drag.samples.shift()
  const n = neighbour(drag.index, drag.count, dx)
  side.value = n == null ? null : dx < 0 ? 'next' : 'prev'
  if (!frame) frame = requestAnimationFrame(paint)
}

// Animate the track to x px, then run done. Instant with reduced motion.
function slide(x: number, done: () => void): void {
  cancelAnimationFrame(frame)
  frame = 0
  settling = true
  const el = track.value
  const ms = window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : settleMs
  if (el) {
    el.style.transition = ms ? `transform ${ms}ms ease-out` : ''
    el.style.transform = `translateX(${x}px)`
  }
  timer = setTimeout(done, ms)
}

// Back at rest: no transform, no clip and no positioning, so the content
// lays out exactly as without the surface.
function reset(): void {
  cancelAnimationFrame(frame)
  frame = 0
  clearTimeout(timer)
  drag = null
  settling = false
  side.value = null
  moving.value = false
  if (track.value) {
    track.value.style.transition = ''
    track.value.style.transform = ''
  }
}

function onUp(e: PointerEvent): void {
  if (!drag || e.pointerId !== drag.id) return
  const d = drag
  drag = null
  if (d.axis !== 'x') return
  clearTimeout(swallowTimer)
  swallowTimer = setTimeout(() => (swallow = false), 400)
  const target = settle({ dx: d.dx, width: d.width, speed: releaseSpeed(d.samples), index: d.index, count: d.count })
  const tab = target == null ? undefined : props.tabs[target]
  if (!tab) {
    slide(0, reset)
    return
  }
  const from = props.active
  slide(d.dx < 0 ? -d.width : d.width, () => {
    // The watch on active resets once the new tab renders; a navigation
    // that did not happen springs back here.
    void router.push(props.to(tab.id)).finally(() => {
      setTimeout(() => {
        if (props.active === from) slide(0, reset)
      })
    })
  })
}

function onCancel(e: PointerEvent): void {
  if (!drag || e.pointerId !== drag.id) return
  const sideways = drag.axis === 'x'
  drag = null
  if (sideways) slide(0, reset)
}

// The click after a sideways drag does not open the link or card under
// the finger.
function onClick(e: MouseEvent): void {
  if (!swallow) return
  swallow = false
  e.preventDefault()
  e.stopPropagation()
}

// A new tab (the swipe's, a tap or Back): drop any drag and show it at rest.
watch(() => props.active, reset, { flush: 'post' })

// Native listeners: as the first Vue invoker on a click's path, the surface
// would stamp it with Date.now(), and under a frozen clock Vue then skips
// the content's handlers (docs/contributing/web-ui.md, "SwipeTabs").
const listeners: [string, (e: never) => void, boolean][] = [
  ['pointerdown', onDown, false],
  ['pointermove', onMove, false],
  ['pointerup', onUp, false],
  ['pointercancel', onCancel, false],
  ['click', onClick, true],
]
onMounted(() => {
  for (const [type, fn, capture] of listeners) root.value?.addEventListener(type, fn as EventListener, capture)
})
onBeforeUnmount(() => {
  for (const [type, fn, capture] of listeners) root.value?.removeEventListener(type, fn as EventListener, capture)
  reset()
  clearTimeout(swallowTimer)
})
</script>

<template>
  <!-- The root keeps the layout of what it wraps (flex column that fills
       its parent), so the inner .scroll scrollers work as before. -->
  <div
    ref="root"
    class="swipe"
    :class="{ live: !disabled, moving }"
    data-swipe-tabs
  >
    <div ref="track" class="track">
      <slot />
      <div v-if="side" class="panel" :class="side" aria-hidden="true" data-swipe-panel>
        <p class="label">{{ panelLabel }}</p>
        <USkeleton v-for="w in lines" :key="w" class="line" :style="{ width: w }" />
      </div>
    </div>
  </div>
</template>

<!-- Scoped CSS, not utilities: it ships in the lazy route's chunk instead
     of the Desk's initial CSS (bundle budget). -->
<style scoped>
.swipe,
.track {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
}
/* While moving: overflow-x: clip, not hidden, clips the panel without
   making the root a scroll container, so the inner scrollers and sticky
   headers keep working; the track holds the panel. */
.swipe.moving {
  overflow-x: clip;
}
.moving > .track {
  position: relative;
}
/* Vertical scrolling stays native. A scroll container lets the browser pan
   both ways again whatever its ancestors say, so the vertical scrollers in
   the slot are pan-y too: the tab's .scroll and every overflow-y-auto
   utility (the Cockpit's pinned column from 1024 px). Sideways scrollers
   still pan sideways; a swipe never starts there. */
.swipe.live,
.live > .track :deep(.scroll),
.live > .track :deep([class*='overflow-y-auto']) {
  touch-action: pan-y;
}
.panel {
  position: absolute;
  inset-block: 0;
  width: 100%;
  padding: 18px 16px 0;
}
.panel.next {
  left: 100%;
}
.panel.prev {
  right: 100%;
}
.label {
  margin-bottom: 16px;
  font-size: 1.125rem;
  font-weight: 600;
  color: var(--text);
}
.line {
  height: 1rem;
  margin-bottom: 12px;
}
@media (min-width: 760px) {
  .panel {
    padding-inline: 28px;
  }
}
</style>
