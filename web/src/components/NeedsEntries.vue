<script setup lang="ts">
import { reactive, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { NeedsEntry } from '../needsCases'
import { pauseTitle } from '../pauseCard'
import { pauseEnd } from '../pauses'
import { promptAnchor, promptOpen, promptStatus, shortStatus } from '../promptCard'
import type { PauseKind } from '../generated/api'
import Truncated from '../ui/Truncated.vue'
import Badge from './Badge.vue'
import Markdown from './Markdown.vue'
import PromptCard from './PromptCard.vue'
import RelTime from './RelTime.vue'

// NeedsEntries is a problem's history (NeedsProblem under Handled, the
// problem card under Open): its prompts, the pilot's actions and its
// pauses, newest first. A closed prompt is a row that unfolds its full
// closed card; an open prompt's row links to its card and has no buttons.
// anchored (Handled): the closed cards carry their #prompt-<id> anchor and
// the one the route hash names unfolds; the Open card's copy has neither,
// so no id is on the page twice.
//
// Untrusted text (questions, summaries, why, lines, pause reasons) is text,
// or markdown without HTML. On phones each fold is 44 px high.
const props = defineProps<{ namespace: string; name: string; entries: NeedsEntry[]; anchored?: boolean }>()

// prompts: the closed prompts whose card is unfolded.
const route = useRoute()
const prompts = reactive(new Set<string>())
function follow() {
  if (!props.anchored) return
  const linked = props.entries.find((e) => e.kind === 'prompt' && route.hash === `#${promptAnchor(e.prompt.id)}`)
  if (linked?.kind === 'prompt') prompts.add(linked.prompt.id)
}
follow()
watch(() => route.hash, follow)

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
  <ul class="m-0 grid list-none gap-1.5 p-0">
    <li v-if="!entries.length" :class="quiet">Nothing recorded yet.</li>
    <li
      v-for="(e, i) in entries"
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
          <PromptCard
            v-if="prompts.has(e.prompt.id)"
            class="mt-1.5"
            :namespace="namespace"
            :name="name"
            :prompt="e.prompt"
            :unanchored="!anchored"
          />
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
</template>
