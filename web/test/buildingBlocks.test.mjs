// The shared building blocks (milestone "Desk: one look", R8; docs/contributing/web-ui.md,
// "Building blocks"): what they replaced stays gone from web/src. npm test.
import assert from 'node:assert/strict'
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { alertColor } from '../src/badgeTone.ts'

const SRC = fileURLToPath(new URL('../src', import.meta.url))

function files(dir) {
  const out = []
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name)
    if (e.isDirectory()) out.push(...files(p))
    else if (/\.(vue|ts|css)$/.test(e.name)) out.push(p)
  }
  return out
}

const rel = (p) => relative(SRC, p).split(sep).join('/')

test('ProgressBar is gone: UProgress draws progress', () => {
  assert.equal(existsSync(join(SRC, 'components', 'ProgressBar.vue')), false)
  const hits = files(SRC)
    .filter((p) => /\bProgressBar\b/.test(readFileSync(p, 'utf8')))
    .map(rel)
  assert.deepEqual(hits, [], 'files that import or name ProgressBar')
})

test('the old tile classes are gone: StatTile draws key figures', () => {
  const hits = files(SRC)
    .filter((p) => /\bclass="[^"]*\bfacts?\b|^\.facts?\b/m.test(readFileSync(p, 'utf8')))
    .map(rel)
  assert.deepEqual(hits, [], 'files with the .facts or .fact tile classes')
})

test('empty states are UEmpty: no "No … yet" or "Nothing …" text in a plain element', () => {
  const hits = files(SRC)
    .filter((p) => p.endsWith('.vue'))
    .flatMap((p) =>
      readFileSync(p, 'utf8')
        .split('\n')
        .map((line, i) => ({ line, at: `${rel(p)}:${i + 1}` }))
        .filter(({ line }) => />\s*(No [^<{]* yet\.?|Nothing [^<{]*)\s*</.test(line)),
    )
    .map((h) => h.at)
  assert.deepEqual(hits, [], 'empty-state texts outside UEmpty (title="…")')
})

// openingTags returns the opening tags of the named elements in a template,
// across lines, with quoted attribute values skipped (an arrow function's
// ">" ends no tag) and comments blanked.
function openingTags(source, names) {
  const s = source.replace(/<!--[\s\S]*?-->/g, (c) => c.replace(/[^\n]/g, ' '))
  const out = []
  const re = new RegExp(`<(${names.join('|')})(?=[\\s>/])`, 'g')
  let m
  while ((m = re.exec(s))) {
    let i = m.index + m[0].length
    let quote = null
    for (; i < s.length; i++) {
      const c = s[i]
      if (quote) {
        if (c === quote) quote = null
      } else if (c === '"' || c === "'") quote = c
      else if (c === '>') break
    }
    const attrs = s.slice(m.index + m[0].length, i)
    out.push({ tag: m[1], attrs, line: s.slice(0, m.index).split('\n').length })
  }
  return out
}

// hasTitle: the tag's attributes (values blanked) carry title= or :title=.
const hasTitle = (attrs) => /(^|\s)(:|v-bind:)?title\s*=/.test(attrs.replace(/"[^"]*"|'[^']*'/g, '""'))

const interactive = ['UButton', 'button', 'a', 'RouterLink', 'ULink', 'ExtLink']

test('openingTags and hasTitle: tags across lines, props of other components are no hits', () => {
  const src = `<template>
  <UPageHeader :title="t" />
  <UEmpty title="Nothing." />
  <UAlert title="Paused" />
  <abbr title="Merge request">MR</abbr>
  <a href="/x" data-x="a > b" class="y"
     :title="hint">x</a>
  <UButton
    :label="l"
    @click="() => go(1)"
    title="Go"
  />
  <button type="button" :aria-label="x">y</button>
  <!-- <RouterLink title="old" /> -->
  <article title="not a link" />
  <RouterLink :to="t" :data-title="x">z</RouterLink>
</template>`
  const hits = openingTags(src, interactive).filter((t) => hasTitle(t.attrs)).map((t) => `${t.tag}:${t.line}`)
  assert.deepEqual(hits, ['a:6', 'UButton:8'])
})

test('hints on buttons and links are UTooltip: no title= or :title= on them', () => {
  const hits = files(SRC)
    .filter((p) => p.endsWith('.vue'))
    .flatMap((p) =>
      openingTags(readFileSync(p, 'utf8'), interactive)
        .filter((t) => hasTitle(t.attrs))
        .map((t) => `${rel(p)}:${t.line} <${t.tag}>`),
    )
  assert.deepEqual(hits, [], 'buttons and links with a title attribute (use UTooltip :text)')
})

test('banners are UAlert: the .banner class is gone', () => {
  const hits = files(SRC)
    .filter((p) => /\bclass="[^"]*\bbanner\b|^\.banner\b/m.test(readFileSync(p, 'utf8')))
    .map(rel)
  assert.deepEqual(hits, [], 'files with the .banner class')
})

// SignIn is in the initial bundle, which badgeTone.ts is not: its one alert
// writes alertColor('danger') as the literal color="error".
const literalColour = new Set(['views/SignIn.vue'])

test('every UAlert is subtle and takes its colour from alertColor', () => {
  assert.equal(alertColor('danger'), 'error')
  const hits = files(SRC)
    .filter((p) => p.endsWith('.vue') && !literalColour.has(rel(p)))
    .flatMap((p) =>
      openingTags(readFileSync(p, 'utf8'), ['UAlert'])
        .filter((t) => !/\svariant="subtle"/.test(t.attrs) || !/\s:color="alertColor\(/.test(t.attrs))
        .map((t) => `${rel(p)}:${t.line}`),
    )
  assert.deepEqual(hits, [], 'UAlerts without variant="subtle" and :color="alertColor(…)"')
})
