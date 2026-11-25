<script setup lang="ts">
import UButton from '@nuxt/ui/components/Button.vue'
import UEmpty from '@nuxt/ui/components/Empty.vue'
import { computed } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import type { NowGroup, NowMilestone, NowRow } from '../nowAndNext'
import Badge from './Badge.vue'

// NowNext is the Now & next sidebar's content (R7 of "Desk: the Cockpit
// newest first by issue"): Running and Waiting, each a link to the issue's
// thread in the Cockpit (rowTo), Up next with "+N more" to the Issues tab
// (moreTo), and the milestones, each a link to its Cockpit (milestoneTo).
// Running and Waiting take rows grouped under headings (NowGroup), so the
// project and the Overview reuse it (D820-P3). Every followed link emits
// follow, so the page closes its slide-over and what it opens shows.
const props = defineProps<{
  running: NowGroup[]
  waiting: NowGroup[]
  next: NowRow[]
  moreNext: number
  moreTo?: RouteLocationRaw
  milestones: NowMilestone[]
  rowTo: (row: NowRow, running: boolean, group: NowGroup) => RouteLocationRaw
  milestoneTo: (iid: number) => RouteLocationRaw
}>()
const emit = defineEmits<{ follow: [] }>()

const count = (groups: NowGroup[]) => groups.reduce((n, g) => n + g.rows.length, 0)
const sections = computed(() =>
  [
    { id: 'running', label: 'Running', groups: props.running, running: true },
    { id: 'waiting', label: 'Waiting', groups: props.waiting, running: false },
  ]
    .map((s) => ({ ...s, count: count(s.groups) }))
    .filter((s) => s.count > 0),
)
const empty = computed(() => !sections.value.length && !props.next.length)
</script>

<template>
  <div class="now" data-now-next>
    <UEmpty v-if="empty" icon="i-lucide-inbox" title="Nothing running" variant="naked" size="xs" data-now-empty />

    <section v-for="s in sections" :key="s.id" :aria-labelledby="`now-${s.id}`" :data-now-section="s.id">
      <h3 :id="`now-${s.id}`">
        <span>{{ s.label }}</span><Badge>{{ s.count }}</Badge>
      </h3>
      <template v-for="g in s.groups" :key="g.key">
        <h4 v-if="g.heading && g.rows.length">{{ g.heading }}</h4>
        <ul v-if="g.rows.length">
          <li v-for="r in g.rows" :key="r.iid">
            <UButton
              :to="rowTo(r, s.running, g)"
              color="neutral"
              variant="ghost"
              size="sm"
              class="row"
              :data-now-row="r.iid"
              @click="emit('follow')"
            >
              <span class="text">{{ r.text }}</span>
              <span class="faint">{{ r.title }}</span>
            </UButton>
          </li>
        </ul>
      </template>
    </section>

    <section v-if="next.length" aria-labelledby="now-next" data-now-section="next">
      <h3 id="now-next">
        <span>Up next</span><Badge>{{ next.length + moreNext }}</Badge>
      </h3>
      <ul>
        <li v-for="r in next" :key="r.iid" class="item" :data-now-row="r.iid">
          <span class="text">{{ r.text }}</span>
          <span class="faint">{{ r.title }}</span>
        </li>
      </ul>
      <UButton
        v-if="moreNext && moreTo"
        :to="moreTo"
        color="neutral"
        variant="link"
        size="sm"
        :label="`+${moreNext} more`"
        class="more"
        data-now-more
        @click="emit('follow')"
      />
    </section>

    <section v-if="milestones.length" aria-labelledby="now-milestones" data-now-section="milestones">
      <h3 id="now-milestones"><span>Milestones</span></h3>
      <ul>
        <li v-for="m in milestones" :key="m.iid" :data-now-milestone="m.iid">
          <UButton
            :to="milestoneTo(m.iid)"
            color="neutral"
            :variant="m.current ? 'soft' : 'ghost'"
            size="sm"
            class="row"
            :aria-current="m.current ? 'page' : undefined"
            @click="emit('follow')"
          >
            <span class="text"><span class="ref">%{{ m.iid }}</span> {{ m.title }}</span>
            <span class="faint">{{ m.current ? `This milestone · ${m.text}` : m.text }}</span>
          </UButton>
        </li>
      </ul>
    </section>
  </div>
</template>

<!-- Scoped, not Tailwind utilities: they load with the page, not with the
     initial stylesheet (docs/contributing/web-ui.md, "Bundle budget"). -->
<style scoped>
.now {
  display: grid;
  gap: 16px;
  min-width: 0;
}
section {
  display: grid;
  gap: 4px;
  min-width: 0;
}
h3 {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin: 0 0 2px;
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-3);
}
h4 {
  margin: 6px 0 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-2);
}
ul {
  display: grid;
  gap: 2px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.row {
  flex-wrap: wrap;
  justify-content: flex-start;
  gap: 0 6px;
  width: 100%;
  height: auto;
  min-height: 44px;
  text-align: start;
  white-space: normal;
}
@media (min-width: 760px) {
  .row {
    min-height: 0;
  }
}
.item {
  display: flex;
  flex-wrap: wrap;
  gap: 0 6px;
  padding: 4px 10px;
  font-size: 13px;
}
.text {
  font-size: 13px;
  color: var(--text);
  overflow-wrap: anywhere;
}
.faint {
  flex-basis: 100%;
  min-width: 0;
  font-size: 12px;
  font-weight: 400;
  color: var(--text-3);
  overflow-wrap: anywhere;
}
.ref {
  font-family: var(--font-mono, ui-monospace, monospace);
  font-size: 12px;
  color: var(--text-3);
}
.more {
  justify-self: start;
}
</style>
