// Every routed view's header is UPageHeader (milestone "Desk: one look",
// decision 4): npm test. The routes come from src/main.ts; a view passes when
// it, or a component in src/components it imports (one level, e.g. Task.vue
// → TaskBody.vue), renders <UPageHeader, and none of them writes an <h1>
// of its own. SignIn.vue is not routed (App.vue shows it before sign-in).
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { stripComments } from './conventions.mjs'

const SRC = fileURLToPath(new URL('../src', import.meta.url))
const read = (file) => stripComments(readFileSync(file, 'utf8'), 'vue')

// routedViews: the files main.ts imports for its route components,
// `const X = () => import('./views/X.vue')`.
function routedViews(main) {
  return [...main.matchAll(/import\(\s*['"](\.\/views\/[^'"]+\.vue)['"]\s*\)/g)].map((m) => m[1])
}

// componentImports: the .vue files in src/components a file imports.
function componentImports(src) {
  return [...src.matchAll(/^\s*import\s+[\w{}\s,]+\s+from\s+['"](\.\.\/components\/[^'"]+\.vue)['"]/gm)].map((m) => m[1])
}

const hasHeader = (src) => /<UPageHeader\b/.test(src)
const hasH1 = (src) => /<h1\b/i.test(src)

test('routedViews reads the lazy view imports of main.ts', () => {
  const main = "const A = () => import('./views/A.vue')\nconst B = () => import(\"./views/B.vue\")\nimport X from './x'"
  assert.deepEqual(routedViews(main), ['./views/A.vue', './views/B.vue'])
})

test('componentImports finds the components a view imports, not others', () => {
  const src = "import TaskBody from '../components/TaskBody.vue'\nimport RouteTabs from '../ui/RouteTabs.vue'\nimport { x } from '../format'"
  assert.deepEqual(componentImports(src), ['../components/TaskBody.vue'])
})

test('every routed view renders its header through UPageHeader, without an <h1>', () => {
  const views = routedViews(readFileSync(join(SRC, 'main.ts'), 'utf8'))
  assert.ok(views.length >= 14, `main.ts routes ${views.length} views`)
  assert.ok(!views.includes('./views/SignIn.vue'), 'SignIn.vue is not routed')
  for (const view of views) {
    const file = resolve(SRC, view)
    assert.ok(existsSync(file), `${view} exists`)
    const src = read(file)
    assert.ok(!hasH1(src), `${view} writes its own <h1>; use UPageHeader`)
    const parts = componentImports(src).map((c) => resolve(dirname(file), c))
    const withHeader = [file, ...parts].filter((f) => hasHeader(read(f)))
    assert.ok(withHeader.length > 0, `${view} has no <UPageHeader>, nor does a component it imports`)
    for (const f of withHeader) assert.ok(!hasH1(read(f)), `${f} writes its own <h1> next to UPageHeader`)
  }
})
