// Bundle budget (docs/ui.md "Bundle budget"): the initial JS plus CSS of the
// built Desk, gzipped at level 9, must stay within bundle-budget.json's capKB.
// Initial is what a cold load fetches before the first route renders: the
// entry module script, its modulepreloads and the stylesheets linked from
// dist/app/index.html. Lazy route chunks do not count.
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

const kB = (bytes) => bytes / 1000
const gz = (file) => gzipSync(readFileSync(file), { level: 9 }).length

function main() {
  const web = join(dirname(fileURLToPath(import.meta.url)), '..')
  const dist = join(web, 'dist', 'app')
  const { capKB } = JSON.parse(readFileSync(join(web, 'bundle-budget.json'), 'utf8'))
  const { js, css } = initialAssets(readFileSync(join(dist, 'index.html'), 'utf8'))
  if (js.length === 0) throw new Error('no entry script in dist/app/index.html')
  const file = (url) => {
    if (/^(?:[a-z]+:)?\/\//i.test(url)) throw new Error(`asset from another origin: ${url}`)
    return join(dist, url.replace(/^\//, ''))
  }
  const jsKB = kB(js.reduce((sum, url) => sum + gz(file(url)), 0))
  const cssKB = kB(css.reduce((sum, url) => sum + gz(file(url)), 0))
  const total = jsKB + cssKB
  console.log(`initial: ${total.toFixed(1)} kB (JS ${jsKB.toFixed(1)}, CSS ${cssKB.toFixed(1)}), cap: ${capKB} kB`)
  if (total > capKB) {
    console.error(`bundle budget exceeded by ${(total - capKB).toFixed(1)} kB (web/bundle-budget.json, docs/ui.md "Bundle budget")`)
    process.exit(1)
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) main()
