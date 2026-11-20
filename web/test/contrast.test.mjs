// Contrast of the colour tokens in src/style.css, light and dark: npm test
// (node --test, no extra dependencies). Text reaches WCAG AA (4.5:1) on the
// three surfaces, white on --primary too; chart series reach 3:1 on --surface
// and keep an OKLab distance from --primary and the danger tone, so no
// series reads as a button or an error (docs/contributing/design.md, "Desk styles").
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const css = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8')

// declarations returns the custom properties of every top-level block whose
// selector is exactly sel (`:root` or `.dark`), later blocks winning.
function declarations(source, sel) {
  const out = {}
  const block = new RegExp(`^${sel.replace('.', '\\.')}\\s*\\{([\\s\\S]*?)^\\}`, 'gm')
  for (const [, body] of source.matchAll(block)) {
    const text = body.replace(/\/\*[\s\S]*?\*\//g, '')
    for (const [, name, value] of text.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) out[name] = value.trim()
  }
  return out
}

// resolve follows var(--x) chains to a #rrggbb colour.
function resolve(vars, value, seen = new Set()) {
  const ref = /^var\((--[\w-]+)\)$/.exec(value)
  if (ref) {
    assert.ok(!seen.has(ref[1]), `var() cycle at ${ref[1]}`)
    assert.ok(ref[1] in vars, `${ref[1]} is not defined`)
    return resolve(vars, vars[ref[1]], new Set([...seen, ref[1]]))
  }
  if (value === 'white') return '#ffffff'
  assert.match(value, /^#[0-9a-f]{6}$/i, `${value} is not a simple colour`)
  return value.toLowerCase()
}

const channels = (hex) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
const linear = (c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)

function luminance(hex) {
  const [r, g, b] = channels(hex).map(linear)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

function oklab(hex) {
  const [r, g, b] = channels(hex).map(linear)
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ]
}

function oklabDistance(a, b) {
  const [x, y] = [oklab(a), oklab(b)]
  return Math.hypot(x[0] - y[0], x[1] - y[1], x[2] - y[2])
}

const light = declarations(css, ':root')
const modes = { light, dark: { ...light, ...declarations(css, '.dark') } }
const color = (vars, name) => resolve(vars, `var(${name})`)

const texts = ['--text', '--text-2', '--text-3']
const surfaces = ['--bg', '--surface', '--surface-2']
const charts = ['--chart-1', '--chart-2', '--chart-3', '--chart-4', '--chart-5', '--chart-6', '--chart-other']

test('the helpers measure known pairs', () => {
  assert.equal(contrast('#ffffff', '#000000'), 21)
  assert.equal(Math.round(contrast('#ffffff', '#c4361f') * 100) / 100, 5.39)
  assert.equal(oklabDistance('#123456', '#123456'), 0)
  assert.ok(Math.abs(oklabDistance('#ffffff', '#000000') - 1) < 1e-3)
})

test('the parser reads both blocks and resolves var() chains', () => {
  assert.equal(light['--primary'], '#c4361f')
  assert.equal(color(light, '--ui-primary'), '#c4361f')
  assert.notEqual(color(modes.dark, '--text-3'), color(light, '--text-3'))
  assert.equal(resolve({ '--a': 'var(--b)', '--b': '#ABCDEF' }, 'var(--a)'), '#abcdef')
  assert.throws(() => resolve({ '--a': 'var(--a)' }, 'var(--a)'), /cycle/)
})

for (const [mode, vars] of Object.entries(modes)) {
  test(`${mode}: text reaches 4.5:1 on every surface`, () => {
    for (const t of texts)
      for (const s of surfaces) {
        const r = contrast(color(vars, t), color(vars, s))
        assert.ok(r >= 4.5, `${t} on ${s}: ${r.toFixed(2)}:1`)
      }
  })

  test(`${mode}: white reaches 4.5:1 on --primary, the colour of the main buttons`, () => {
    assert.equal(color(vars, '--ui-primary'), color(vars, '--primary'))
    const r = contrast('#ffffff', color(vars, '--primary'))
    assert.ok(r >= 4.5, `white on --primary: ${r.toFixed(2)}:1`)
  })

  test(`${mode}: each chart series reaches 3:1 on --surface`, () => {
    for (const c of charts) {
      const r = contrast(color(vars, c), color(vars, '--surface'))
      assert.ok(r >= 3, `${c} on --surface: ${r.toFixed(2)}:1`)
    }
  })

  test(`${mode}: no chart series sits near --primary or the danger tone`, () => {
    const danger = color(vars, '--ui-error')
    assert.equal(danger, color(vars, '--tone-danger-fg'))
    for (const c of charts)
      for (const [name, other] of [
        ['--primary', color(vars, '--primary')],
        ['danger', danger],
      ]) {
        const d = oklabDistance(color(vars, c), other)
        assert.ok(d >= 0.1, `${c} to ${name}: OKLab ${d.toFixed(3)}`)
      }
  })

  test(`${mode}: Other is a grey of its own, not --text-3`, () => {
    assert.notEqual(color(vars, '--chart-other'), color(vars, '--text-3'))
  })
}
