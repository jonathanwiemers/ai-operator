<script setup lang="ts">
import { computed, ref } from 'vue'
import { session } from '../../api'
import { clockTime, fullTime } from '../../format'
import { useNow } from '../../live'
import type { StreamEvent } from '../../types'
import ExtLink from '../ExtLink.vue'
import Markdown from '../Markdown.vue'

const props = defineProps<{ notes: StreamEvent[] }>()
const now = useNow()
const expanded = ref(false)

const me = computed(() => session.me?.username)
const newest = computed(() => props.notes[props.notes.length - 1]!)
const hidden = computed(() => Math.max(0, props.notes.length - 3))
const visible = computed(() => (expanded.value ? props.notes : props.notes.slice(-3)))

function isMe(e: StreamEvent) {
  return !!me.value && e.actor === me.value && !e.data.bot
}
function isPilot(e: StreamEvent) {
  return !!e.data.bot
}
function ctx(e: StreamEvent) {
  if (e.data.noteable === 'mr' && e.mergeRequestIID) return `!${e.mergeRequestIID}`
  if (e.issueIID) return `#${e.issueIID}`
  return ''
}
</script>

<template>
  <div class="ev">
    <div class="av" :class="{ me: isMe(newest), pilot: isPilot(newest) }">
      {{ isPilot(newest) ? 'P' : isMe(newest) ? 'me' : (newest.actor ?? '?').slice(0, 2).toUpperCase() }}
    </div>
    <div>
      <div class="meta">
        <b>{{ isPilot(newest) ? 'Pilot' : isMe(newest) ? 'you' : newest.actor ? `@${newest.actor}` : 'Comment' }}</b>
        <span v-if="ctx(newest)" class="ctx">{{ ctx(newest) }}</span>
        <time :datetime="newest.at" :title="fullTime(newest.at)">{{ clockTime(newest.at, now) }}</time>
      </div>
      <button v-if="hidden > 0 && !expanded" type="button" class="thread-toggle" @click="expanded = true">
        {{ hidden }} earlier
      </button>
      <div v-for="n in visible" :key="n.id" class="bubble" :class="{ me: isMe(n), pilot: isPilot(n) }">
        <div v-if="visible.length > 1" class="meta tight">
          <b>{{ isPilot(n) ? 'Pilot' : isMe(n) ? 'you' : n.actor ? `@${n.actor}` : 'Comment' }}</b>
          <time :datetime="n.at" :title="fullTime(n.at)">{{ clockTime(n.at, now) }}</time>
        </div>
        <template v-if="n.redacted">
          <p class="redacted">
            comment by {{ n.actor || 'someone' }}
            <template v-if="ctx(n)"> on {{ ctx(n) }}</template>
            <ExtLink v-if="n.data.url" :href="n.data.url">open</ExtLink>
          </p>
        </template>
        <Markdown v-else-if="n.data.body" :text="n.data.body" />
        <p v-else>{{ n.summary }}</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ev {
  display: grid;
  grid-template-columns: 34px minmax(0, 1fr);
  gap: 10px;
  margin-bottom: 14px;
}
.av {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 11px;
  font-weight: 700;
  color: #fff;
  background: var(--brand-ink);
}
.av.pilot {
  background: linear-gradient(135deg, var(--accent), var(--accent-2));
}
.av.me {
  background: #6b5bd6;
}
.meta {
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
  font-size: 12.5px;
  color: var(--text-3);
  margin-bottom: 4px;
}
.meta.tight {
  margin-bottom: 2px;
}
.meta b {
  color: var(--text);
  font-weight: 600;
  font-size: 13.5px;
}
.ctx {
  color: var(--text-2);
}
.bubble {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  padding: 12px 14px;
  margin-bottom: 8px;
}
.bubble:last-child {
  margin-bottom: 0;
}
.bubble.me {
  background: var(--me-bg);
  border-color: var(--me-border);
}
.bubble.pilot {
  background: var(--pilot-bg);
  border-color: var(--pilot-border);
}
.bubble p {
  margin: 0;
}
.redacted {
  color: var(--text-2);
  font-size: 13.5px;
}
.thread-toggle {
  border: 0;
  background: none;
  color: var(--link);
  font: inherit;
  font-size: 12.5px;
  cursor: pointer;
  padding: 0;
  margin-bottom: 6px;
}
</style>
