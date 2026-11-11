<script setup lang="ts">
import { computed, ref } from 'vue'
import { clockTime, fullTime } from '../format'
import { useNow } from '../live'
import { foldNotes, noteInitials, noteWho, type ThreadNote } from '../noteThread'
import ExtLink from './ExtLink.vue'
import Markdown from './Markdown.vue'

// NoteThread is the one renderer of a GitLab comment thread (docs/ui.md,
// "Note thread"): each note's author, time and block markdown body, the
// user's and the pilot's notes marked by colour.
//
// In full (the task page's conversation): every note with its own author
// line, system notes faded.
//
// compact (the stream's threads): an avatar and the newest note's author
// and issue or MR, the last three notes and "N earlier" to show the rest.
//
// Replying is the caller's: the conversation's form, the stream's composer.
const props = defineProps<{ notes: ThreadNote[]; compact?: boolean }>()
const now = useNow()
const expanded = ref(false)

const newest = computed(() => props.notes[props.notes.length - 1])
const fold = computed(() => foldNotes(props.notes, props.compact ? 3 : undefined, expanded.value))

const tone = (n: ThreadNote) =>
  n.bot ? 'bg-(--pilot-bg) border-(--pilot-border)' : n.mine ? 'bg-(--me-bg) border-(--me-border)' : 'bg-(--surface) border-(--border)'
</script>

<template>
  <div class="grid gap-2.5" :class="compact ? 'mb-3.5 grid-cols-[34px_minmax(0,1fr)]' : 'p-3'">
    <div
      v-if="compact && newest"
      class="grid size-7.5 place-items-center rounded-full text-[11px] font-bold text-white"
      :class="newest.bot ? 'bg-linear-135 from-(--accent) to-(--accent-2)' : newest.mine ? 'bg-(--me-avatar)' : 'bg-(--brand-ink)'"
    >
      {{ noteInitials(newest) }}
    </div>
    <div class="grid min-w-0 gap-2">
      <div v-if="compact && newest" class="flex flex-wrap items-baseline gap-2 text-[12.5px] text-(--text-3)">
        <b class="text-[13.5px] font-semibold text-(--text)">{{ noteWho(newest) }}</b>
        <span v-if="newest.ctx" class="text-(--text-2)">{{ newest.ctx }}</span>
        <time :datetime="newest.at" :title="fullTime(newest.at)">{{ clockTime(newest.at, now) }}</time>
      </div>
      <button
        v-if="fold.hidden > 0"
        type="button"
        class="cursor-pointer justify-self-start border-0 bg-transparent p-0 text-left font-[inherit] text-[12.5px] text-(--link)"
        @click="expanded = true"
      >
        {{ fold.hidden }} earlier
      </button>
      <div
        v-for="n in fold.visible"
        :key="n.id"
        class="min-w-0 rounded-(--radius) border px-3.5 py-3 shadow-(--shadow)"
        :class="[tone(n), { 'text-[13px] opacity-60': n.system }]"
        :data-note="n.id"
      >
        <div
          v-if="!compact || fold.visible.length > 1"
          class="mb-0.5 flex flex-wrap items-baseline gap-2 text-[12.5px] text-(--text-3)"
        >
          <b class="text-[13.5px] font-semibold text-(--text)">{{ noteWho(n) }}</b>
          <time v-if="n.at" :datetime="n.at" :title="fullTime(n.at)">{{ clockTime(n.at, now) }}</time>
        </div>
        <p v-if="n.redacted" class="m-0 text-[13.5px] text-(--text-2)">
          comment by {{ n.author || 'someone' }}
          <template v-if="n.ctx"> on {{ n.ctx }}</template>
          <ExtLink v-if="n.url" :href="n.url">open</ExtLink>
        </p>
        <Markdown v-else-if="n.body" class="[overflow-wrap:anywhere]" :text="n.body" />
        <p v-else class="m-0"><Markdown mode="inline" :text="n.summary ?? ''" /></p>
      </div>
    </div>
  </div>
</template>
