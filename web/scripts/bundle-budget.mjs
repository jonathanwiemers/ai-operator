// Bundle budget (docs/ui.md "Bundle budget"): the initial JS plus CSS of the
// built Desk, gzipped at level 9, must stay within bundle-budget.json's capKB.
// Initial is what a cold load fetches before the first route renders: the
// entry module script, its modulepreloads and the stylesheets linked from
// dist/app/index.html. Lazy route chunks do not count there.
//
// Each route in bundle-budget.json's routes is checked on its own: the JS
// and CSS its view loads beyond the initial set, found in Vite's build
// manifest (dist/app/.vite/manifest.json) by following the view's static
// and dynamic imports, must stay within the route's capKB.
//
//   node scripts/bundle-budget.mjs   (npm run budget, part of make ui)
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'

function attrs(tag) {
  const out = {}
  for (const m of tag.matchAll(/([\w:-]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?/g)) {
    out[m[1].toLowerCase()] = m[2] ?? m[3] ?? m[4] ?? ''
  }
  return out
}

// initialAssets returns the paths of the initial JS and CSS that index.html
// references, in document order and without duplicates.
export function initialAssets(html) {
  const js = []
  const css = []
  const add = (list, url) => {
    if (url && !js.includes(url) && !css.includes(url)) list.push(url)
  }
  for (const m of html.matchAll(/<(script|link)\b([^>]*)>/gi)) {
    const a = attrs(m[2])
    if (m[1].toLowerCase() === 'script') {
      if (a.type === 'module') add(js, a.src)
      continue
    }
    const rel = (a.rel ?? '').toLowerCase().split(/\s+/)
    if (rel.includes('modulepreload')) add(js, a.href)
    else if (rel.includes('stylesheet')) add(css, a.href)
  }
  return { js, css }
}

// routeAssets returns the JS and CSS files (paths relative to dist/app) that
// the manifest's entry loads and the initial set does not: its chunk, the
// chunks it imports statically or dynamically, transitively, and their CSS,
// stopping at chunks of the initial set.
// initial holds paths as initialAssets returns them, with or without a
// leading slash.
export function routeAssets(manifest, entry, initial = []) {
  if (!manifest[entry]) throw new Error(`${entry} is not in the build manifest`)
  const skip = new Set(initial.map((u) => u.replace(/^\//, '')))
  const js = []
  const css = []
  const seen = new Set()
  const add = (list, file) => {
    if (!skip.has(file) && !js.includes(file) && !css.includes(file)) list.push(file)
  }
  const visit = (key) => {
    if (seen.has(key)) return
    seen.add(key)
    const chunk = manifest[key]
    if (!chunk) throw new Error(`${key} is not in the build manifest`)
    // An initial chunk is loaded already, and its dynamic imports (the
    // entry's are every route) are not this route's.
    if (skip.has(chunk.file)) return
    add(js, chunk.file)
    for (const file of chunk.css ?? []) add(css, file)
    for (const k of [...(chunk.imports ?? []), ...(chunk.dynamicImports ?? [])]) visit(k)
  }
  visit(entry)
  return { js, css }
}

const kB = (bytes) => bytes / 1000
const gz = (file) => gzipSync(readFileSync(file), { level: 9 }).length

function main() {
  const web = join(dirname(fileURLToPath(import.meta.url)), '..')
  const dist = join(web, 'dist', 'app')
  const budget = JSON.parse(readFileSync(join(web, 'bundle-budget.json'), 'utf8'))
  const { js, css } = initialAssets(readFileSync(join(dist, 'index.html'), 'utf8'))
  if (js.length === 0) throw new Error('no entry script in dist/app/index.html')
  const file = (url) => {
    if (/^(?:[a-z]+:)?\/\//i.test(url)) throw new Error(`asset from another origin: ${url}`)
    return join(dist, url.replace(/^\//, ''))
  }
  const size = (urls) => kB(urls.reduce((sum, url) => sum + gz(file(url)), 0))
  let failed = false
  const check = (name, assets, capKB) => {
    const jsKB = size(assets.js)
    const cssKB = size(assets.css)
    const total = jsKB + cssKB
    console.log(`${name}: ${total.toFixed(1)} kB (JS ${jsKB.toFixed(1)}, CSS ${cssKB.toFixed(1)}), cap: ${capKB} kB`)
    if (total > capKB) {
      console.error(`${name} over its budget by ${(total - capKB).toFixed(1)} kB (web/bundle-budget.json, docs/ui.md "Bundle budget")`)
      failed = true
    }
  }
  check('initial', { js, css }, budget.capKB)
  const routes = Object.entries(budget.routes ?? {})
  if (routes.length > 0) {
    const manifest = JSON.parse(readFileSync(join(dist, '.vite', 'manifest.json'), 'utf8'))
    for (const [name, route] of routes) check(`route ${name}`, routeAssets(manifest, route.entry, [...js, ...css]), route.capKB)
  }
  if (failed) process.exit(1)
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main()
