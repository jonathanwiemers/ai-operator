// Conventions check over web/src: npm test. Report-only rules print their
// hits and pass; set `enforce: true` to make a rule fail the test.
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import {
  apiCalls,
  htmlInjection,
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
    if (rule.enforce) assert.deepEqual(hits, [])
  })
}

test('matchers: colours', () => {
  assert.deepEqual(rawColours('a { color: #fff; }\nb { c: #a1b2c3d4 }\nd { e: rgba(0,0,0,.5) }'), [1, 2, 3])
  assert.deepEqual(rawColours('a { color: var(--fg) } &#123;'), [])
  assert.deepEqual(rawColours(stripComments('/* #fff */\na { c: red } // #000', 'ts')), [])
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
