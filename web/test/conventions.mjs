// Matchers of the conventions check (conventions.test.mjs).

// Blank out comments, keeping line structure, so a colour or call named in a
// comment is not a hit. Strings are left alone (a colour is often one).
export function stripComments(src, kind) {
  const blank = (m) => m.replace(/[^\n]/g, ' ')
  let out = src.replace(/\/\*[\s\S]*?\*\//g, blank)
  if (kind === 'vue') out = out.replace(/<!--[\s\S]*?-->/g, blank)
  // `//` line comments, but not the one in `https://`.
  out = out.replace(/(^|[^:\w"'`])\/\/[^\n]*/g, (m, p) => p + blank(m.slice(p.length)))
  return out
}

const COLOUR = /#[0-9a-fA-F]{8}\b|#[0-9a-fA-F]{6}\b|#[0-9a-fA-F]{3,4}\b|\b(?:rgba?|hsla?|oklch)\(/g
const HTML = /v-html|\binnerHTML\b|\bouterHTML\b|\binsertAdjacentHTML\b/g
const MEDIA_WIDTH = /@media[^{]*/g
// The three widths (docs/contributing/design.md, "Widths"): a tablet or desktop rule
// starts at 760 or 1180 px, a rule below one ends a hundredth under it, so the
// two never match together (Tailwind's tablet: and desktop: are min-width).
const ALLOWED_WIDTHS = { min: new Set(['760', '1180']), max: new Set(['759.98', '1179.98']) }

// Names the file imports from api.ts, among get and post (maybe renamed).
export function apiHelpers(src) {
  const names = new Set()
  const re = /import\s*\{([^}]*)\}\s*from\s*['"][^'"]*\/api(?:\.ts)?['"]/g
  for (const m of src.matchAll(re)) {
    for (const part of m[1].split(',')) {
      const [orig, alias] = part.trim().split(/\s+as\s+/)
      if (orig === 'get' || orig === 'post') names.add(alias || orig)
    }
  }
  return names
}

function lineOf(src, index) {
  let n = 1
  for (let i = 0; i < index; i++) if (src.charCodeAt(i) === 10) n++
  return n
}

function scan(src, re, filter) {
  const hits = []
  for (const m of src.matchAll(re)) {
    if (!filter || filter(m)) hits.push(lineOf(src, m.index))
  }
  return hits
}

// Blank out quoted strings in the <script> blocks of a .vue file (keeping line
// structure), so a tag named in a string literal is not a hit. The template's
// attributes are quoted too, so it is left alone.
function blankScriptStrings(src) {
  const blank = (m) => m.replace(/[^\n]/g, ' ')
  const strings = /'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"|`(?:[^`\\]|\\.)*`/g
  return src.replace(/(<script\b[^>]*>)([\s\S]*?)(<\/script>)/g, (_, a, body, c) => a + body.replace(strings, blank) + c)
}

const RAW_TAG = /<(?:button|select|textarea|progress)\b|<input\b[^>]*?\btype\s*=\s*["']?(?:checkbox|radio|range)\b/g

// Raw form controls in a .vue file, which Nuxt UI has components for. Run it
// on stripComments output.
export function rawControls(src) {
  return scan(blankScriptStrings(src), RAW_TAG)
}

export function htmlInjection(src) {
  return scan(src, HTML)
}

export function rawColours(src) {
  // `#` followed by hex in a URL fragment or an id selector is rare in
  // these files; `&#123;`-style entities are skipped.
  return scan(src, COLOUR, (m) => src[m.index - 1] !== '&')
}

export function apiCalls(src) {
  const hits = new Set()
  for (const l of scan(src, /\b(?:get|post)</g, (m) => !/function\s+$/.test(src.slice(Math.max(0, m.index - 12), m.index)) && src[m.index - 1] !== '.')) hits.add(l)
  const names = apiHelpers(src)
  const alt = ['get', 'post', ...names].join('|')
  for (const l of scan(src, new RegExp(`await\\s+(?:${alt})\\s*\\(`, 'g'))) hits.add(l)
  for (const n of names) {
    for (const l of scan(src, new RegExp(`(?<![.\\w])${n}\\s*\\(`, 'g'))) hits.add(l)
  }
  return [...hits].sort((a, b) => a - b)
}

export function strayBreakpoints(src) {
  const hits = []
  for (const m of src.matchAll(MEDIA_WIDTH)) {
    for (const w of m[0].matchAll(/(min|max)-width\s*:\s*(\d+(?:\.\d+)?)px/g)) {
      if (!ALLOWED_WIDTHS[w[1]].has(w[2])) hits.push(lineOf(src, m.index))
    }
    // A width in px that is not in a min-width or max-width feature.
    const all = m[0].match(/\d+(?:\.\d+)?px/g) ?? []
    const named = m[0].match(/(?:min|max)-width\s*:\s*\d+(?:\.\d+)?px/g) ?? []
    if (all.length > named.length) hits.push(lineOf(src, m.index))
  }
  return [...new Set(hits)]
}

// A positive tabindex (Decision 6 of docs/specs/desk-accessible.spec.md):
// the tab order follows the page's order. As an attribute (`tabindex="1"`,
// `:tabindex="2"`), a property (`el.tabIndex = 1`) or an argument
// (`setAttribute('tabindex', '1')`); 0 and -1 are fine.
const TABINDEX = /\btab[iI]ndex["']?\s*[=:,]\s*["'`]?\s*\+?0*[1-9]/g

export function positiveTabindex(src) {
  return scan(src, TABINDEX)
}

// A live region of its own (Decision 7 of docs/specs/desk-accessible.spec.md):
// components announce through useAnnounce(); App.vue renders its two regions.
// aria-live="off" (turning a role's implicit one off) is fine.
const LIVE = /\baria-live\s*=\s*["']?(?!off\b)\w/g

export function liveRegions(src) {
  return scan(src, LIVE)
}
