// Tests of components/LocalText.vue: npm test (node --test, no extra
// dependencies). Node cannot import a .vue file, so this renders what
// LocalText renders (localTimeNodes) and checks the component's source and
// the places that print plain server text.
process.env.TZ = 'Europe/Berlin'

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { localTimeNodes } from '../src/localTimeNodes.ts'

const src = (p) => readFileSync(new URL(`../src/${p}`, import.meta.url), 'utf8')
const hm = (iso) => new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit' }).format(new Date(iso))

test('LocalText renders localTimeNodes with the reactive now, in no wrapper', () => {
  const vue = src('components/LocalText.vue')
  assert.match(vue, /localTimeNodes\(props\.text, now\.value\)/)
  assert.match(vue, /useNow\(\)/)
  assert.doesNotMatch(vue, /<template>/, 'a render function, no wrapper element')
})

test('a reason with one UTC time renders the local label in a <time>', async () => {
  // Saturday, 21 Nov 2026, 14:00 CET.
  const now = Date.parse('2026-11-21T13:00:00Z')
  const reason = 'claude usage limit, resets 2026-11-21 14:05 UTC'
  const app = createSSRApp({ render: () => h('span', localTimeNodes(reason, now)) })
  const html = await renderToString(app)
  assert.ok(html.startsWith('<span>claude usage limit, resets <time datetime="2026-11-21T14:05:00.000Z"'), html)
  assert.ok(html.endsWith(`>${hm('2026-11-21T14:05:00Z')}</time></span>`), html)
  assert.equal(new Date('2026-11-21T14:05:00Z').getHours(), 15)
  assert.match(html, /title="[^"]+ · 2026-11-21 14:05 UTC as written"/)
})

// Each place that prints a server reason, message, summary, detail or cause
// without Markdown, and what it passes to LocalText.
const places = {
  'components/MilestoneNeeds.vue': ['p.cause'],
  'components/PauseCard.vue': ['e.detail', 'answer.reason'],
  'components/CommandStatus.vue': ['answer.reason'],
  'components/ChatTurn.vue': ['message.reason'],
  'components/TaskBody.vue': ['t.status.agentTermination.reason', 'c.message', 'health.stalledReason'],
  'components/QueueList.vue': ["q.hold.reason || 'Held.'"],
  'components/ProjectQueue.vue': ['answer.reason'],
  'components/stream/EventCard.vue': ['e.summary', 'e.summary || e.kind'],
  'views/Research.vue': ['r.run.reason', 'r.skippedReason || r.run?.reason', 'r.skippedReason', 'r.run.message'],
  'views/Researches.vue': ['r.run.reason', 'r.skippedReason'],
  'views/ResearchRun.vue': ['run.choiceReason'],
  'views/ResearchNew.vue': ['selected.reason'],
}

test('plain server text goes through LocalText', () => {
  for (const [file, fields] of Object.entries(places)) {
    const vue = src(file)
    const tags = vue.match(/<LocalText [^>]*\/>/g) ?? []
    for (const f of fields) assert.ok(tags.some((t) => t.endsWith(`:text="${f}" />`)), `${file}: ${f}`)
    assert.doesNotMatch(vue, /\{\{[^}]*(\.reason|\.message|\.summary|\.detail|\.cause|Reason) \}\}/, `${file}: a raw interpolation`)
  }
})

test('the truncated cause in MilestoneNeeds has its full local text as title', () => {
  assert.match(src('components/MilestoneNeeds.vue'), /:title="localTimesText\(p\.cause, now\)"/)
})
