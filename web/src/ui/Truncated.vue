<script setup lang="ts">
// Text clamped to `lines` lines that opens on tap (docs/ui.md, "Truncated").
// Only text that is really clipped becomes a button: it toggles the clamp in
// place, with aria-expanded. There is no `title`: it cannot be read on touch.
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = withDefaults(defineProps<{ text?: string; lines?: number }>(), { text: undefined, lines: 1 })

const el = ref<HTMLElement | null>(null)
const clipped = ref(false)
const open = ref(false)

// Static class names, so Tailwind sees them.
const clamps = ['truncate', 'line-clamp-2', 'line-clamp-3', 'line-clamp-4', 'line-clamp-5', 'line-clamp-6']
const clamp = computed(() => clamps[Math.min(Math.max(Math.round(props.lines), 1), clamps.length) - 1])

// measure runs only while clamped: an open element is not clipped, and keeps
// its button so it can be closed again.
function measure(): void {
  const e = el.value
  if (!e || open.value) return
  clipped.value = e.scrollWidth > e.clientWidth || e.scrollHeight > e.clientHeight
}

let resize: ResizeObserver | undefined
onMounted(() => {
  measure()
  if (typeof ResizeObserver !== 'undefined' && el.value) {
    resize = new ResizeObserver(measure)
    resize.observe(el.value)
  }
})
onBeforeUnmount(() => resize?.disconnect())
watch(() => [props.text, props.lines], () => nextTick(measure), { flush: 'post' })

function toggle(): void {
  if (clipped.value) open.value = !open.value
}
</script>

<template>
  <div
    ref="el"
    :class="[open ? 'break-words whitespace-normal' : clamp, clipped && 'cursor-pointer']"
    :role="clipped ? 'button' : undefined"
    :tabindex="clipped ? 0 : undefined"
    :aria-expanded="clipped ? open : undefined"
    @click="toggle"
    @keydown.enter.prevent="toggle"
    @keydown.space.prevent="toggle"
  >
    <slot>{{ text }}</slot>
  </div>
</template>
