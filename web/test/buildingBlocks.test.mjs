// The shared building blocks (milestone "Desk: one look", R8; docs/contributing/web-ui.md,
// "Building blocks"): what they replaced stays gone from web/src. npm test.
import assert from 'node:assert/strict'
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'

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
