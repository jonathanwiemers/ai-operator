// Conventions check over web/src: npm test. Report-only rules print their
// hits and pass; set `enforce: true` to make a rule fail the test.
import assert from 'node:assert/strict'
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import {
  apiCalls,
  htmlInjection,
  liveRegions,
  positiveTabindex,
  rawControls,
  rawColours,
  stripComments,
  strayBreakpoints,
} from './conventions.mjs'

const SRC = fileURLToPath(new URL('../src', import.meta.url))

// `skip`: files (relative to src, with /) where the rule does not apply.
const RULES = [
  { name: 'html-injection', enforce: true, find: htmlInjection, skip: [] },
  { name: 'raw-colour', enforce: true, find: rawColours, skip: ['style.css'] },
  { name: 'api-call-outside-client', enforce: true, find: apiCalls, skip: ['client.ts'] },
  { name: 'breakpoint', enforce: true, find: strayBreakpoints, skip: [] },
  // Nuxt UI first (docs/contributing/web-ui.md, "UI kit"): skip only the
  // files of documented custom components that need a raw control.
  { name: 'raw-control', enforce: true, find: rawControls, skip: [] },
  // The tab order follows the page (Decision 6 of
  // docs/specs/desk-accessible.spec.md): no tabindex above 0.
  { name: 'positive-tabindex', enforce: true, find: positiveTabindex, skip: [] },
  // One announcer (Decision 7): no aria-live but its regions in App.vue.
  { name: 'ad-hoc-live-region', enforce: true, find: liveRegions, skip: ['App.vue'] },
]

function files(dir) {
  const out = []
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name)
    if (e.isDirectory()) {
      if (e.name !== 'generated') out.push(...files(p))
    } else if (/\.(vue|ts|css)$/.test(e.name)) out.push(p)
  }
  return out
}

function hitsOf(rule) {
  const hits = []
  for (const f of files(SRC)) {
    const rel = relative(SRC, f).split(sep).join('/')
    if (rule.skip.includes(rel)) continue
    const ext = f.slice(f.lastIndexOf('.') + 1)
    // Colours and breakpoints are a CSS matter, calls and HTML a script one.
    if (rule.name === 'raw-colour' || rule.name === 'breakpoint') {
      if (ext === 'ts') continue
    } else if (rule.name === 'raw-control') {
      if (ext !== 'vue') continue
    } else if (ext === 'css') continue
    const src = stripComments(readFileSync(f, 'utf8'), ext)
    for (const line of rule.find(src)) hits.push(`${rel}:${line} ${rule.name}`)
  }
  return hits
}

for (const rule of RULES) {
  test(`conventions: ${rule.name}${rule.enforce ? '' : ' (report only)'}`, (t) => {
    const hits = hitsOf(rule)
    for (const h of hits) t.diagnostic(h)
    t.diagnostic(`${rule.name}: ${hits.length} hit(s)`)
    if (!rule.enforce) for (const h of hits) console.warn(`warning: ${h}`)
    if (rule.enforce) assert.deepEqual(hits, [])
  })
}

// A skip entry names a file that exists: a renamed or deleted component
// does not leave a stale exception behind.
test('conventions: skip entries exist', () => {
  for (const rule of RULES) {
    for (const rel of rule.skip) assert.ok(existsSync(join(SRC, ...rel.split('/'))), `${rule.name} skips a missing file: ${rel}`)
  }
})

// A raw <button> in a view fails npm test.
test('conventions: raw-control is enforced on views', () => {
  const rule = RULES.find((r) => r.name === 'raw-control')
  assert.equal(rule.enforce, true)
  assert.ok(!rule.skip.some((f) => f.startsWith('views/')), 'no view is skipped')
  assert.deepEqual(rule.find('<template>\n  <button type="button" @click="go">Go</button>\n</template>'), [2])
})

test('matchers: colours', () => {
  assert.deepEqual(rawColours('a { color: #fff; }\nb { c: #a1b2c3d4 }\nd { e: rgba(0,0,0,.5) }'), [1, 2, 3])
  assert.deepEqual(rawColours('a { color: var(--fg) } &#123;'), [])
  assert.deepEqual(rawColours(stripComments('/* #fff */\na { c: red } // #000', 'ts')), [])
})

test('matchers: raw controls', () => {
  const tpl = (body) => `<template>\n${body}\n</template>`
  assert.deepEqual(rawControls(tpl('<button>a</button>\n<button class="x">b</button>')), [2, 3])
  assert.deepEqual(rawControls(tpl('<select>\n</select>\n<textarea />\n<progress />')), [2, 4, 5])
  assert.deepEqual(rawControls(tpl('<input type="checkbox">\n<input class="a" type="range">\n<input type=radio>')), [2, 3, 4])
  assert.deepEqual(rawControls(tpl('<UButton />\n<USelect />\n<UTextarea />\n<buttonish />\n<input type="text">\n<input>')), [])
  assert.deepEqual(rawControls(stripComments(tpl('<!-- <button> -->\n<p/>'), 'vue')), [])
  assert.deepEqual(rawControls("<script setup>\nconst a = '<button>'\nconst b = `x\n<select>`\n</script>\n<template><p/></template>"), [])
})

test('matchers: html injection', () => {
  assert.deepEqual(htmlInjection('<div v-html="x"/>\nel.innerHTML = 1'), [1, 2])
  assert.deepEqual(htmlInjection('const s = "safe"'), [])
})

test('matchers: api calls', () => {
  assert.deepEqual(apiCalls('const m = new Map()\nm.get(k)\nmap.get<number>(1)'), [])
  assert.deepEqual(apiCalls('const x = await get<T>(p)'), [1])
  assert.deepEqual(apiCalls('x = post<T>(p, b)'), [1])
  assert.deepEqual(apiCalls('export async function get<T>(p: string) {}'), [])
  assert.deepEqual(apiCalls("import { get as fetchIt } from '../api'\nawait fetchIt(p)"), [2])
  assert.deepEqual(apiCalls("import { post } from '../api'\nawait post(p, b)"), [2])
  assert.deepEqual(apiCalls('await get(p)'), [1])
  // Prose in a template is not a call.
  assert.deepEqual(apiCalls('<p>Then post <code>/agent</code> commands.</p>'), [])
})

test('matchers: positive tabindex', () => {
  const tpl = (body) => `<template>\n${body}\n</template>`
  assert.deepEqual(positiveTabindex(tpl('<p tabindex="1" />\n<p :tabindex="2" />\n<p tabindex=3 />')), [2, 3, 4])
  assert.deepEqual(positiveTabindex("el.tabIndex = 1\nel.setAttribute('tabindex', '4')\nh({ tabindex: 10 })"), [1, 2, 3])
  assert.deepEqual(positiveTabindex(tpl('<p tabindex="0" />\n<p tabindex="-1" />\n<p :tabindex="open ? 0 : undefined" />')), [])
  assert.deepEqual(positiveTabindex("h.setAttribute('tabindex', '-1')\nel.tabIndex = 0"), [])
})

test('matchers: live regions', () => {
  const tpl = (body) => `<template>\n${body}\n</template>`
  assert.deepEqual(liveRegions(tpl('<p aria-live="polite" />\n<p aria-live="assertive" />\n<p :aria-live="x" />')), [2, 3, 4])
  assert.deepEqual(liveRegions(tpl('<div role="log" aria-live="off" />')), [])
})

test('matchers: breakpoints', () => {
  const css = [
    '@media (max-width: 759.98px) {}',
    '@media (min-width: 1180px) {}',
    '@media (max-width: 640px) {}',
    '@media (max-width: 760px) {}',
    '@media (min-width: 759.98px) {}',
    '@media (min-width: 760px) and (max-width: 1179.98px) {}',
    '@media (width < 900px) {}',
    '@media (prefers-reduced-motion: reduce) {}',
  ].join('\n')
  assert.deepEqual(strayBreakpoints(css), [3, 4, 5, 7])
})
