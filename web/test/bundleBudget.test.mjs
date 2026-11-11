// Tests of scripts/bundle-budget.mjs: npm test.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { initialAssets } from '../scripts/bundle-budget.mjs'

test('initialAssets collects the entry, modulepreloads and stylesheets', () => {
  const html = `<!doctype html>
<html><head>
  <link rel="icon" type="image/svg+xml" href="/logo.svg" />
  <script type="module" crossorigin src="/assets/index-a1.js"></script>
  <link rel="modulepreload" crossorigin href="/assets/vue-b2.js">
  <link rel='stylesheet' crossorigin href='/assets/index-c3.css'>
  <link rel="modulepreload" href="/assets/vue-b2.js">
</head><body><div id="app"></div><script>void 0</script></body></html>`
  assert.deepEqual(initialAssets(html), {
    js: ['/assets/index-a1.js', '/assets/vue-b2.js'],
    css: ['/assets/index-c3.css'],
  })
})

test('initialAssets finds nothing in a page without assets', () => {
  assert.deepEqual(initialAssets('<html><body></body></html>'), { js: [], css: [] })
})
