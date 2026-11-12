// Tests of scripts/bundle-budget.mjs: npm test.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { initialAssets, routeAssets } from '../scripts/bundle-budget.mjs'

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

// A manifest as Vite writes it: index.html is the entry, Costs.vue a lazy
// route that shares one chunk with the initial set and one with another view.
const manifest = {
  'index.html': {
    file: 'assets/index-a1.js',
    isEntry: true,
    imports: ['_vue-b2.js'],
    dynamicImports: ['src/views/Costs.vue', 'src/views/Dashboard.vue'],
    css: ['assets/index-c3.css'],
  },
  '_vue-b2.js': { file: 'assets/vue-b2.js' },
  '_chart-d4.js': { file: 'assets/chart-d4.js', imports: ['_vue-b2.js'], css: ['assets/chart-e5.css'] },
  '_lazy-f6.js': { file: 'assets/lazy-f6.js', isDynamicEntry: true },
  'src/views/Costs.vue': {
    file: 'assets/Costs-g7.js',
    isDynamicEntry: true,
    imports: ['_vue-b2.js', '_chart-d4.js', 'index.html'],
    dynamicImports: ['_lazy-f6.js'],
    css: ['assets/Costs-h8.css'],
  },
  'src/views/Dashboard.vue': { file: 'assets/Dashboard-i9.js', isDynamicEntry: true, imports: ['_chart-d4.js'] },
}

test('routeAssets follows the route\'s imports beyond the initial set', () => {
  const initial = ['/assets/index-a1.js', '/assets/vue-b2.js', '/assets/index-c3.css']
  assert.deepEqual(routeAssets(manifest, 'src/views/Costs.vue', initial), {
    js: ['assets/Costs-g7.js', 'assets/chart-d4.js', 'assets/lazy-f6.js'],
    css: ['assets/Costs-h8.css', 'assets/chart-e5.css'],
  })
})

test('routeAssets counts shared chunks when nothing is initial', () => {
  assert.deepEqual(routeAssets(manifest, 'src/views/Dashboard.vue'), {
    js: ['assets/Dashboard-i9.js', 'assets/chart-d4.js', 'assets/vue-b2.js'],
    css: ['assets/chart-e5.css'],
  })
})

test('routeAssets refuses an entry the build does not have', () => {
  assert.throws(() => routeAssets(manifest, 'src/views/Nope.vue'), /not in the build manifest/)
})
