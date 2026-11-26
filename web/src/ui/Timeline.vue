<script setup lang="ts">
// The issue timeline (docs/contributing/web-ui.md, "UI kit"): groups of
// labelled rows on a time axis, laid out by timeline.ts. The labels stay on
// the left while the axis scrolls sideways; each row is a list of bars, and
// each bar is a link with its full state as its name (Decision 5, D666-P1).
// HTML and CSS only: nothing in Nuxt UI draws a time axis, and a canvas
// cannot be reached by keyboard.
import type { ChartToken } from '../chartData'
import { segmentKey, type LegendEntry, type SegmentLayout, type Tick, type TimelineBarItem, type TimelineGroupItem } from '../timeline'

defineProps<{
  groups: TimelineGroupItem[]
  ticks: Tick[]
  nowPct: number | null
  // legend: the milestones by colour (the Overview), shown first in the
  // key, before the segment kinds (D830-P3).
  legend?: Pick<LegendEntry, 'key' | 'title' | 'token'>[]
}>()

// Static class names, so Tailwind sees them. The label column and the
// track's right margin, which leaves room for a label that runs past a bar
// ending at now.
const labelCol = 'sticky left-0 z-10 w-28 shrink-0 bg-(--surface) tablet:w-56'
const trackBox = 'relative mr-10 flex-1'
// The Issues tab's fill: the kind's tone bg. The stripe is the kind's
// --segment-* token on both views (stripe below).
const tone: Record<SegmentLayout['kind'], string> = {
  running: 'bg-(--tone-progress-bg)',
  review: 'bg-(--tone-info-bg)',
  ci: 'bg-(--tone-neutral-bg)',
  waiting: 'bg-(--tone-muted-bg)',
  held: 'bg-(--tone-warning-bg)',
}
// On the Overview a segment takes its milestone's colour, stronger while
// working, mixed into the surface so the label keeps its contrast.
const mix: Record<SegmentLayout['kind'], number> = { running: 40, review: 40, ci: 40, held: 25, waiting: 12 }
// Waiting is hatched over its fill, so it reads as part of the bar and not
// as a gap (D830-P2). Inline, so it adds nothing to the global stylesheet.
const hatch = 'repeating-linear-gradient(45deg, color-mix(in srgb, var(--segment-waiting) 30%, transparent) 0 2px, transparent 2px 6px)'

function stripe(kind: SegmentLayout['kind']) {
  return { borderColor: `var(--segment-${kind})`, backgroundImage: kind === 'waiting' ? hatch : undefined }
}

function barStyle(b: TimelineBarItem) {
  return { left: `${b.bar.left}%`, width: `${b.bar.width}%` }
}

function segStyle(s: SegmentLayout, token?: ChartToken) {
  const at = { left: `${s.left}%`, width: `${s.width}%`, ...stripe(s.kind) }
  if (!token) return at
  return { ...at, backgroundColor: `color-mix(in srgb, var(${token}) ${mix[s.kind]}%, var(--surface))` }
}
</script>

<template>
  <!-- A milestone legend (the Overview) comes first, above the axis; the
       segment key comes after the bars. -->
  <div class="flex flex-col">
    <div class="overflow-x-auto rounded-(--radius-sm) border border-(--border) bg-(--surface)" data-timeline>
      <div class="relative min-w-[40rem] pb-1.5">
        <!-- The grid lines and the now line, behind the bars. -->
        <div class="pointer-events-none absolute inset-y-0 right-10 left-28 tablet:left-56" aria-hidden="true">
          <span v-for="t in ticks" :key="t.pct" class="absolute inset-y-0 w-px bg-(--border)" :style="{ left: `${t.pct}%` }" />
          <span
            v-if="nowPct !== null"
            class="absolute inset-y-0 z-[1] w-0.5 -translate-x-1/2 bg-(--text-2)"
            :style="{ left: `${nowPct}%` }"
            data-now
          />
        </div>
        <div class="flex h-7 border-b border-(--border)" aria-hidden="true" data-axis>
          <div :class="labelCol" />
          <div :class="trackBox">
            <span
              v-for="t in ticks"
              :key="t.pct"
              class="absolute top-1.5 -translate-x-1/2 text-[11px] whitespace-nowrap text-(--text-3) tabular-nums"
              :style="{ left: `${t.pct}%` }"
            >
              {{ t.label }}
            </span>
          </div>
        </div>
        <template v-for="g in groups" :key="g.key">
          <div v-if="g.heading" class="flex">
            <h3
              class="sticky left-0 z-10 m-0 bg-(--surface) px-3 pt-2.5 pb-0.5 text-[11.5px] font-semibold tracking-[0.06em] text-(--text-3) uppercase"
              data-wave
            >
              {{ g.heading }}
            </h3>
          </div>
          <div v-for="r in g.rows" :key="r.key" class="flex" data-timeline-row>
            <div :class="labelCol" class="flex min-w-0 items-baseline gap-1.5 px-3 pt-2 text-[12.5px]" :title="r.sub || (r.to ? r.label : undefined)">
              <RouterLink v-if="r.to" :to="r.to" class="truncate font-medium" data-timeline-row-link>{{ r.label }}</RouterLink>
              <template v-else>
                <span class="font-medium whitespace-nowrap text-(--text-3) tabular-nums">{{ r.label }}</span>
                <span v-if="r.sub" class="truncate">{{ r.sub }}</span>
              </template>
            </div>
            <div :class="trackBox">
              <ul
                v-for="(line, n) in r.lines"
                :key="n"
                class="relative m-0 h-8 list-none p-0"
                :aria-label="r.sub ? `${r.label} ${r.sub}` : r.label"
              >
                <li v-for="b in line" :key="b.key" class="absolute top-1 h-6 min-w-2" :style="barStyle(b)" data-bar>
                  <component
                    :is="b.to ? 'RouterLink' : 'span'"
                    :to="b.to || undefined"
                    :role="b.to ? undefined : 'img'"
                    class="relative flex h-full items-center overflow-visible text-(--text) no-underline"
                    :class="[b.bar.cutStart ? 'rounded-l-none' : 'rounded-l-sm', b.bar.cutEnd ? 'rounded-r-none' : 'rounded-r-sm']"
                    :aria-label="b.name"
                    :title="b.name"
                  >
                    <span
                      v-for="(s, i) in b.bar.segments"
                      :key="i"
                      class="absolute inset-y-0 border-b-[3px]"
                      :class="b.token ? undefined : tone[s.kind]"
                      :style="segStyle(s, b.token)"
                      :data-segment="s.kind"
                      aria-hidden="true"
                    />
                    <span class="relative px-1 text-[11px] font-medium whitespace-nowrap tabular-nums">{{ b.label }}</span>
                    <span
                      v-if="b.bar.marker"
                      class="absolute top-1/2 size-2.5 -translate-x-1/2 -translate-y-1/2 rotate-45 border-2 border-(--surface)"
                      :class="b.bar.marker.kind === 'merged' ? 'bg-(--tone-success-fg)' : 'bg-(--tone-muted-fg)'"
                      :style="{ left: `${b.bar.marker.left}%` }"
                      :data-marker="b.bar.marker.kind"
                      aria-hidden="true"
                    />
                  </component>
                </li>
              </ul>
            </div>
          </div>
        </template>
      </div>
    </div>
    <ul
      class="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-[12px] text-(--text-2)"
      :class="legend?.length ? 'order-first mb-2' : 'mt-2'"
      data-timeline-key
    >
      <li v-for="l in legend" :key="l.key" class="flex items-center gap-1.5" data-legend>
        <span class="size-2.5 rounded-sm" :style="{ background: `var(${l.token})` }" aria-hidden="true" />{{ l.title }}
      </li>
      <li v-for="k in segmentKey" :key="k.kind" class="flex items-center gap-1.5" :data-key="k.kind">
        <span class="h-2.5 w-4 rounded-sm border-b-[3px]" :class="tone[k.kind]" :style="stripe(k.kind)" aria-hidden="true" />{{ k.label }}
      </li>
      <li class="flex items-center gap-1.5">
        <span class="size-2 rotate-45 bg-(--tone-success-fg)" aria-hidden="true" />Merged
      </li>
      <li class="flex items-center gap-1.5">
        <span class="size-2 rotate-45 bg-(--tone-muted-fg)" aria-hidden="true" />Skipped
      </li>
    </ul>
  </div>
</template>
