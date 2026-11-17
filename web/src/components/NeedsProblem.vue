<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { problemAnchor, type Problem } from '../needsCases'
import { pauseTitle } from '../pauseCard'
import { pauseEnd } from '../pauses'
import { promptAnchor, promptOpen, promptStatus, shortStatus } from '../promptCard'
import type { PauseKind } from '../generated/api'
import Truncated from '../ui/Truncated.vue'
import Badge from './Badge.vue'
import Markdown from './Markdown.vue'
import PromptCard from './PromptCard.vue'
import RelTime from './RelTime.vue'

// NeedsProblem is one problem of the Needs you tab's Handled section (R14):
// an issue, or the milestone (iid 0), with its prompts, the pilot's actions
// and its pauses, newest first. Open problems and the one the route hash
// names (#problem-<iid>, or #prompt-<id> of one of its prompts) start
// unfolded; the chevron button folds the entries. The heading is no
// <summary>: a summary's children are presentational, so the issue link
// would not be a link there. A closed prompt is a row that unfolds its full closed card; an
// open prompt's row links up to its card under Open and has no buttons.
//
// Untrusted text (questions, summaries, why, lines, pause reasons) is text,
// or markdown without HTML. Utilities only (docs/contributing/web-ui.md,
// "Needs you tab"): on phones the chevron and each fold are 44 px high.
const props = defineProps<{ namespace: string; name: string; milestone: number; problem: Problem; linked: boolean }>()

const route = useRoute()
const anchor = computed(() => problemAnchor(props.problem.iid))
const issueTo = computed(() => ({
  name: 'issue' as const,
  params: { namespace: props.namespace, name: props.name, iid: String(props.milestone), issue: String(props.problem.iid) },
}))

// unfolded: the problem's fold; prompts: the closed prompts whose card is
// unfolded. Both follow the user's clicks and open when the hash names them.
const unfolded = ref(props.problem.open || props.linked)
const prompts = reactive(new Set<string>())
const el = ref<HTMLElement | null>(null)
// heading: "#N title". Built in script: a leading space inside a
// <template> is condensed away, which glued the title to the number.
const heading = computed(() => [`#${props.problem.iid}`, props.problem.title].filter(Boolean).join(' '))
const what = computed(() => (props.problem.iid ? `#${props.problem.iid}` : 'the milestone'))

function follow() {
  if (props.linked) unfolded.value = true
  const linkedPrompt = props.problem.entries.find((e) => e.kind === 'prompt' && route.hash === `#${promptAnchor(e.prompt.id)}`)
  if (linkedPrompt?.kind === 'prompt') prompts.add(linkedPrompt.prompt.id)
  if (route.hash === `#${anchor.value}`) void nextTick(() => el.value?.scrollIntoView({ block: 'start' }))
}
follow()
watch(() => [route.hash, props.linked], follow)
// A problem that turns open while the page is live (a new prompt, a hold)
// unfolds, so its open row is in sight.
watch(
  () => props.problem.open,
  (open) => {
    if (open) unfolded.value = true
  },
)

function toggled(id: string, e: Event) {
  if ((e.target as HTMLDetailsElement).open) prompts.add(id)
  else prompts.delete(id)
}

const kindTitle = (kind: string) => pauseTitle(kind as PauseKind)
const foldRow = 'min-h-11 cursor-pointer tablet:min-h-0'
const row = 'min-w-0 rounded-lg bg-(--surface-2) px-2.5 py-2'
const quiet = 'text-[12.5px] text-(--text-3)'
const chevron = 'inline-block text-(--text-3) transition-transform motion-reduce:transition-none'
</script>

<template>
  <section
    :id="anchor"
    ref="el"
    class="card min-w-0 scroll-mt-3 px-3.5 py-2.5 text-[13.5px]"
    :data-problem="problem.iid"
    :data-open="unfolded ? '' : undefined"
  >
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1" data-problem-head>
      <button
        type="button"
        class="-ms-1.5 inline-flex size-11 cursor-pointer items-center justify-center rounded-(--radius-sm) tablet:size-6"
        :aria-expanded="unfolded"
        :aria-controls="`${anchor}-entries`"
        :aria-label="`Entries of ${what}`"
        data-problem-toggle
        @click="unfolded = !unfolded"
      >
        <span aria-hidden="true" :class="[chevron, unfolded && 'rotate-90']">▸</span>
      </button>
      <RouterLink v-if="problem.iid" :to="issueTo" class="min-w-0 font-semibold wrap-anywhere">
        {{ heading }}
      </RouterLink>
      <b v-else>Milestone</b>
      <Badge :tone="problem.open ? 'warning' : 'neutral'">{{ problem.open ? 'Open' : 'Handled' }}</Badge>
      <span v-if="problem.attempts" class="ms-auto" :class="quiet" data-attempts>
        Pilot attempts: {{ problem.attempts.used }} of {{ problem.attempts.max }}
      </span>
    </div>

    <ul v-show="unfolded" :id="`${anchor}-entries`" class="m-0 mt-2.5 grid list-none gap-1.5 p-0">
      <li v-if="!problem.entries.length" :class="quiet">Nothing recorded yet.</li>
      <li
        v-for="(e, i) in problem.entries"
        :key="e.kind === 'prompt' ? e.prompt.id : `${e.kind}-${i}`"
        class="min-w-0"
        :data-entry="e.kind"
      >
        <template v-if="e.kind === 'prompt'">
          <!-- An open prompt: one row that leads to its card under Open. -->
          <div v-if="promptOpen(e.prompt)" class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1" :class="row">
            <RouterLink :to="{ hash: `#${promptAnchor(e.prompt.id)}` }"><code>{{ e.prompt.id }}</code></RouterLink>
            <span class="min-w-0 flex-1 truncate"><Markdown mode="inline" :text="e.prompt.question" /></span>
            <Badge :tone="promptStatus(e.prompt).tone">{{ shortStatus(e.prompt) }}</Badge>
            <span class="basis-full" :class="quiet">{{ promptStatus(e.prompt).text }}</span>
          </div>
          <details v-else class="group/row" :open="prompts.has(e.prompt.id)" @toggle="toggled(e.prompt.id, $event)">
            <summary class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1" :class="[row, foldRow]">
              <span aria-hidden="true" :class="[chevron, 'group-open/row:rotate-90']">▸</span>
              <code>{{ e.prompt.id }}</code>
              <span class="min-w-0 flex-1 truncate"><Markdown mode="inline" :text="e.prompt.question" /></span>
              <Badge :tone="promptStatus(e.prompt).tone">{{ shortStatus(e.prompt) }}</Badge>
              <span class="basis-full" :class="quiet">
                {{ promptStatus(e.prompt).text }} · <RelTime :time="e.at" />
              </span>
            </summary>
            <PromptCard class="mt-1.5" :namespace="namespace" :name="name" :prompt="e.prompt" />
          </details>
        </template>

        <div v-else-if="e.kind === 'acted'" class="grid gap-1" :class="row">
          <div class="flex flex-wrap items-baseline gap-x-2">
            <b>{{ e.acted.by ? `@${e.acted.by}` : 'Pilot' }}</b>
            <span v-if="!e.acted.ran" class="text-(--tone-warning-fg)">ran none of its actions</span>
            <RelTime class="ms-auto" :class="quiet" :time="e.acted.at" />
          </div>
          <p v-if="e.acted.summary" class="m-0 font-[550] wrap-anywhere">{{ e.acted.summary }}</p>
          <p v-if="e.acted.why" class="m-0 whitespace-pre-line text-(--text-2) wrap-anywhere">{{ e.acted.why }}</p>
          <Markdown v-if="e.acted.lines?.length" class="text-[12.5px] wrap-anywhere" :text="e.acted.lines.join('\n')" />
        </div>

        <div v-else class="grid gap-0.5" :class="[row, !e.pause.until && 'bg-(--tone-warning-bg)']">
          <div class="flex flex-wrap items-baseline gap-x-2">
            <b>{{ kindTitle(e.pause.kind) }}</b>
            <span class="ms-auto" :class="quiet">
              <template v-if="e.pause.until"><RelTime :time="e.pause.since" /> – <RelTime :time="e.pause.until" /></template>
              <template v-else>since <RelTime :time="e.pause.since" /></template>
            </span>
          </div>
          <Truncated v-if="e.pause.reason" class="text-[12.5px] text-(--text-2)">
            <Markdown mode="inline" :text="e.pause.reason" />
          </Truncated>
          <span v-if="e.pause.until" :class="quiet">{{ pauseEnd(e.pause) }}</span>
        </div>
      </li>
    </ul>
  </section>
</template>
