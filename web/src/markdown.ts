import { h, type VNode, type VNodeChild } from 'vue'
import { safeURL } from './format.ts'

// A markdown subset rendered straight to VNodes: agent text never becomes an
// HTML string, so whatever it contains shows as text. Supported are
// paragraphs, headings (as h3–h5), lists (with task items), blockquotes,
// tables, rules, details/summary, emphasis, strong, strikethrough, code
// spans, fenced code, entities, a few attribute-free inline tags and links to
// http(s) URLs; HTML comments are dropped and everything else is text.

// LinkFn renders a link to a safe http(s) URL.
export type LinkFn = (href: string, label: VNodeChild[]) => VNodeChild

// Nesting deeper than this (blockquotes, lists, emphasis) stays text.
const maxDepth = 12
// How far a closing delimiter, bracket or backtick run is looked for.
const maxSpan = 1000

const fenceRe = /^( {0,3})(`{3,}|~{3,})(.*)$/
const headingRe = /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$/
const quoteRe = /^ {0,3}> ?/
const itemRe = /^( {0,3})([-*+]|\d{1,9}[.)])([ \t]+|$)(.*)$/
const hrRe = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/
const detailsRe = /^ {0,3}<details(\s+open)?\s*>/i
const detailsTagRe = /<(\/?)details(?:\s+open)?\s*>/gi
const taskRe = /^\[([ xX])\](?:[ \t]+|$)/

export function renderMarkdown(src: string, link: LinkFn): VNode[] {
  return blocks(src.replace(/\r\n?/g, '\n').split('\n'), link, 0)
}

function startsBlock(line: string): boolean {
  return (
    fenceRe.test(line) || headingRe.test(line) || quoteRe.test(line) || itemRe.test(line) || hrRe.test(line) || detailsRe.test(line)
  )
}

const indentOf = (line: string) => line.length - line.trimStart().length

function blocks(lines: string[], link: LinkFn, depth: number): VNode[] {
  const out: VNode[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]!
    if (line.trim() === '') {
      i++
      continue
    }
    const fence = fenceRe.exec(line)
    if (fence && !(fence[2]![0] === '`' && fence[3]!.includes('`'))) {
      const indent = fence[1]!.length
      const marker = fence[2]!
      const code: string[] = []
      for (i++; i < lines.length; i++) {
        const l = lines[i]!
        const t = l.trim()
        if (t.length >= marker.length && t.startsWith(marker) && t.replaceAll(marker[0]!, '') === '') {
          i++
          break
        }
        code.push(l.replace(new RegExp(`^ {0,${indent}}`), ''))
      }
      out.push(h('pre', h('code', code.join('\n'))))
      continue
    }
    if (hrRe.test(line)) {
      out.push(h('hr'))
      i++
      continue
    }
    if (depth < maxDepth && detailsRe.test(line)) {
      i = details(lines, i, link, depth, out)
      continue
    }
    if (tableAt(lines, i)) {
      i = table(lines, i, link, out)
      continue
    }
    const heading = headingRe.exec(line)
    if (heading) {
      out.push(h(`h${Math.min(heading[1]!.length + 2, 5)}`, inline(heading[2] ?? '', link, 0)))
      i++
      continue
    }
    if (depth < maxDepth && quoteRe.test(line)) {
      const inner: string[] = []
      for (; i < lines.length && quoteRe.test(lines[i]!); i++) inner.push(lines[i]!.replace(quoteRe, ''))
      out.push(h('blockquote', blocks(inner, link, depth + 1)))
      continue
    }
    const item = itemRe.exec(line)
    if (depth < maxDepth && item) {
      i = list(lines, i, link, depth, out)
      continue
    }
    const para: string[] = [line.trim()]
    for (i++; i < lines.length && lines[i]!.trim() !== '' && !startsBlock(lines[i]!) && !tableAt(lines, i); i++) {
      para.push(lines[i]!.trim())
    }
    const content = inline(para.join('\n'), link, 0)
    // A paragraph of only an HTML comment leaves nothing to show.
    if (content.some((c) => typeof c !== 'string' || c.trim() !== '')) out.push(h('p', content))
  }
  return out
}

// details renders the <details> block that starts at lines[start] into out
// and returns the index of the line to go on with. It ends at the matching
// </details> (or the end of the text); text after that on the same line is
// left in lines for the caller.
function details(lines: string[], start: number, link: LinkFn, depth: number, out: VNode[]): number {
  const open = detailsRe.exec(lines[start]!)!
  const body: string[] = []
  let line = lines[start]!.slice(open[0].length)
  let level = 1
  let i = start
  for (;;) {
    detailsTagRe.lastIndex = 0
    let m: RegExpExecArray | null
    while ((m = detailsTagRe.exec(line)) && (level += m[1] ? -1 : 1) > 0);
    if (m) {
      body.push(line.slice(0, m.index))
      const rest = line.slice(m.index + m[0].length)
      if (rest.trim() !== '') lines[i] = rest
      else i++
      break
    }
    body.push(line)
    if (++i >= lines.length) break
    line = lines[i]!
  }
  let text = body.join('\n')
  const children: VNodeChild[] = []
  const summary = /^\s*<summary>/i.exec(text)
  const close = summary ? text.toLowerCase().indexOf('</summary>', summary[0].length) : -1
  if (summary && close >= 0) {
    children.push(h('summary', inline(text.slice(summary[0].length, close).trim(), link, 0)))
    text = text.slice(close + '</summary>'.length)
  }
  children.push(...blocks(text.split('\n'), link, depth + 1))
  out.push(h('details', open[1] ? { open: true } : null, children))
  return i
}

// cells splits a table row at its unescaped pipes; \| is a pipe in a cell.
function cells(line: string): string[] {
  let s = line.trim()
  if (s.startsWith('|')) s = s.slice(1)
  if (s.endsWith('|') && !s.endsWith('\\|')) s = s.slice(0, -1)
  const out: string[] = []
  let cur = ''
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '\\' && s[i + 1] === '|') {
      cur += '|'
      i++
    } else if (s[i] === '|') {
      out.push(cur.trim())
      cur = ''
    } else {
      cur += s[i]
    }
  }
  out.push(cur.trim())
  return out
}

// tableAt reports whether a table starts at lines[i]: a row with a pipe and
// a delimiter row with as many cells.
function tableAt(lines: string[], i: number): boolean {
  const head = lines[i]!
  const delim = lines[i + 1]
  if (!head.includes('|') || delim === undefined || !delim.includes('-')) return false
  const d = cells(delim)
  return d.every((c) => /^:?-+:?$/.test(c)) && d.length === cells(head).length
}

// table renders the table that starts at lines[start] into out and returns
// the index of the first line after it; a blank line or a block ends it.
function table(lines: string[], start: number, link: LinkFn, out: VNode[]): number {
  const head = cells(lines[start]!)
  const align = cells(lines[start + 1]!).map((c) =>
    c.endsWith(':') ? { style: { textAlign: c.startsWith(':') ? 'center' : 'right' } } : c.startsWith(':') ? { style: { textAlign: 'left' } } : null,
  )
  const row = (cs: string[], tag: 'th' | 'td') => h('tr', head.map((_, k) => h(tag, align[k], inline(cs[k] ?? '', link, 0))))
  const rows: VNode[] = []
  let i = start + 2
  for (; i < lines.length && lines[i]!.trim() !== '' && !startsBlock(lines[i]!); i++) rows.push(row(cells(lines[i]!), 'td'))
  out.push(h('div', { class: 'table' }, h('table', [h('thead', row(head, 'th')), ...(rows.length ? [h('tbody', rows)] : [])])))
  return i
}

// list renders the list that starts at lines[start] into out and returns the
// index of the first line after it. An item's further lines are indented to
// its text or continue its paragraph.
function list(lines: string[], start: number, link: LinkFn, depth: number, out: VNode[]): number {
  // Items of one list share their bullet, or the delimiter after their number.
  const kind = (m: RegExpExecArray | null) => m && (/\d/.test(m[2]!) ? m[2]!.slice(-1) : m[2]!)
  const first = itemRe.exec(lines[start]!)!
  const items: string[][] = []
  const tasks: (boolean | null)[] = []
  let width = 0
  let i = start
  while (i < lines.length) {
    const line = lines[i]!
    if (i > start && hrRe.test(line) && indentOf(line) < width) break
    const m = itemRe.exec(line)
    if (m && kind(m) === kind(first) && m[1]!.length < width + (items.length ? 0 : 4)) {
      width = m[1]!.length + m[2]!.length + Math.min(Math.max(m[3]!.length, 1), 4)
      const task = taskRe.exec(m[4]!)
      tasks.push(task ? task[1] !== ' ' : null)
      items.push([task ? m[4]!.slice(task[0].length) : m[4]!])
      i++
      continue
    }
    const indent = indentOf(line)
    const cur = items[items.length - 1]!
    if (line.trim() === '') {
      // Blank lines end the list unless an item goes on after them.
      let j = i + 1
      while (j < lines.length && lines[j]!.trim() === '') j++
      const next = lines[j]
      if (next === undefined) break
      if (indentOf(next) < width && kind(itemRe.exec(next)) !== kind(first)) break
      cur.push('')
      i = j
      continue
    } else if (indent >= width) {
      cur.push(line.slice(width))
    } else if (cur[cur.length - 1] !== '' && !startsBlock(line)) {
      cur.push(line.trim())
    } else {
      break
    }
    i++
  }
  const children = items.map((it, k) =>
    tasks[k] === null
      ? h('li', blocks(it, link, depth + 1))
      : h('li', { class: 'task' }, [h('input', { type: 'checkbox', disabled: true, checked: tasks[k] }), ...blocks(it, link, depth + 1)]),
  )
  const n = Number.parseInt(first[2]!, 10)
  out.push(/\d/.test(first[2]!) ? h('ol', n !== 1 ? { start: n } : null, children) : h('ul', children))
  return i
}

const punct = /[!-/:-@[-`{-~]/

// Inline HTML tags rendered as elements, only without attributes.
const inlineTags = new Map([
  ['code', 'code'],
  ['kbd', 'kbd'],
  ['sub', 'sub'],
  ['sup', 'sup'],
  ['b', 'strong'],
  ['strong', 'strong'],
  ['i', 'em'],
  ['em', 'em'],
  ['s', 'del'],
  ['del', 'del'],
])

const namedEntities = new Map([
  ['amp', '&'],
  ['lt', '<'],
  ['gt', '>'],
  ['quot', '"'],
  ['apos', "'"],
  ['nbsp', '\u00a0'],
  ['hellip', '…'],
  ['mdash', '—'],
  ['ndash', '–'],
])
const entityRe = /&(?:#(\d{1,7})|#[xX]([0-9a-fA-F]{1,6})|([a-zA-Z]{2,8}));/y

// entity decodes the entity matched by entityRe, or is undefined for an
// unknown name.
function entity(dec: string | undefined, hex: string | undefined, name: string | undefined): string | undefined {
  if (name !== undefined) return namedEntities.get(name)
  const cp = dec !== undefined ? Number.parseInt(dec, 10) : Number.parseInt(hex!, 16)
  return cp === 0 || cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff) ? '\ufffd' : String.fromCodePoint(cp)
}

const decodeEntities = (s: string) =>
  s.replace(new RegExp(entityRe.source, 'g'), (m, dec, hex, name) => entity(dec, hex, name) ?? m)

// tagAt matches an HTML comment (rendered as nothing), <br> or an allowlisted
// inline tag with its closing tag at i.
function tagAt(s: string, i: number, link: LinkFn | null, depth: number): { node: VNodeChild; end: number } | null {
  if (s.startsWith('<!--', i)) {
    const end = s.slice(i + 4, i + 4 + maxSpan).indexOf('-->')
    return end < 0 ? null : { node: null, end: i + 4 + end + 3 }
  }
  const br = /^<br\s*\/?>/i.exec(s.slice(i, i + 8))
  if (br) return { node: h('br'), end: i + br[0].length }
  const m = /^<([a-z]{1,6})>/i.exec(s.slice(i, i + 9))
  const name = m?.[1]!.toLowerCase()
  const el = name && inlineTags.get(name)
  if (!el || depth >= maxDepth) return null
  const from = i + m![0].length
  const close = s.slice(from, from + maxSpan).toLowerCase().indexOf(`</${name}>`)
  if (close < 0) return null
  const inner = s.slice(from, from + close)
  const node = el === 'code' || el === 'kbd' ? h(el, decodeEntities(inner)) : h(el, inline(inner, link, depth + 1))
  return { node, end: from + close + name.length + 3 }
}

// inline renders emphasis, strong, strikethrough, code spans, inline tags,
// entities and links; the rest is text.
function inline(s: string, link: LinkFn | null, depth: number): VNodeChild[] {
  const out: VNodeChild[] = []
  let text = ''
  const flush = () => {
    if (text) out.push(text)
    text = ''
  }
  for (let i = 0; i < s.length; ) {
    const ch = s[i]!
    if (ch === '\\' && i + 1 < s.length && punct.test(s[i + 1]!)) {
      text += s[i + 1]
      i += 2
      continue
    }
    if (ch === '`') {
      const n = run(s, i)
      const end = closingRun(s, i + n, n, '`')
      if (end >= 0) {
        let code = s.slice(i + n, end).replaceAll('\n', ' ')
        if (code.length > 2 && code.startsWith(' ') && code.endsWith(' ')) code = code.slice(1, -1)
        flush()
        out.push(h('code', code))
        i = end + n
      } else {
        text += s.slice(i, i + n)
        i += n
      }
      continue
    }
    if (ch === '[' && link && depth < maxDepth) {
      const l = linkAt(s, i)
      if (l) {
        flush()
        const label = inline(l.label, null, depth + 1)
        const href = safeURL(l.href)
        if (href) out.push(link(href, label))
        else out.push(...label)
        i = l.end
        continue
      }
    }
    if (ch === '<') {
      const t = tagAt(s, i, link, depth)
      if (t) {
        flush()
        if (t.node) out.push(t.node)
        i = t.end
        continue
      }
    }
    if (ch === '&') {
      entityRe.lastIndex = i
      const m = entityRe.exec(s)
      const decoded = m && entity(m[1], m[2], m[3])
      if (decoded !== undefined && decoded !== null) {
        text += decoded
        i += m![0].length
        continue
      }
    }
    if (ch === '~' && depth < maxDepth) {
      const n = run(s, i)
      const end = n === 2 ? closingRun(s, i + 2, 2, '~') : -1
      if (end > i + 2 && !/\s/.test(s[i + 2]!) && !/\s/.test(s[end - 1]!)) {
        flush()
        out.push(h('del', inline(s.slice(i + 2, end), link, depth + 1)))
        i = end + 2
      } else {
        text += s.slice(i, i + n)
        i += n
      }
      continue
    }
    if ((ch === '*' || ch === '_') && depth < maxDepth) {
      const e = emphasisAt(s, i)
      if (e) {
        flush()
        const inner = inline(e.inner, link, depth + 1)
        out.push(e.n === 1 ? h('em', inner) : e.n === 2 ? h('strong', inner) : h('strong', [h('em', inner)]))
        i = e.end
        continue
      }
      const n = run(s, i)
      text += s.slice(i, i + n)
      i += n
      continue
    }
    text += ch
    i++
  }
  flush()
  return out
}

// run is the length of the run of s[i] at i.
function run(s: string, i: number): number {
  let j = i
  while (j < s.length && s[j] === s[i]) j++
  return j - i
}

// closingRun returns the index of the first run of exactly n ch at or after
// from (within maxSpan), or -1.
function closingRun(s: string, from: number, n: number, ch: string): number {
  for (let j = from; j < s.length && j < from + maxSpan; ) {
    if (s[j] !== ch) {
      j++
      continue
    }
    const r = run(s, j)
    if (r === n) return j
    j += r
  }
  return -1
}

// emphasisAt matches *em*, **strong** or ***both*** (or with _) at i: the
// opener is not followed and the closer not preceded by whitespace, and _
// does not open inside a word.
function emphasisAt(s: string, i: number): { n: number; inner: string; end: number } | null {
  const ch = s[i]!
  if (ch === '_' && i > 0 && /[\p{L}\p{N}]/u.test(s[i - 1]!)) return null
  const n = Math.min(run(s, i), 3)
  const from = i + n
  if (from >= s.length || /\s/.test(s[from]!)) return null
  const window = s.slice(from, from + maxSpan)
  // The closer is a run of exactly n, so the ** of **b** in *a **b** c* is not.
  for (let j = window.indexOf(ch, 1); j > 0; j = window.indexOf(ch, j)) {
    const r = run(window, j)
    const after = window[j + r]
    if (r === n && !/[\s\\]/.test(window[j - 1]!) && !(ch === '_' && after && /[\p{L}\p{N}]/u.test(after))) {
      return { n, inner: window.slice(0, j), end: from + j + n }
    }
    j += r
  }
  return null
}

// linkAt matches [label](destination "title") at i.
function linkAt(s: string, i: number): { label: string; href: string; end: number } | null {
  let depth = 0
  let close = -1
  for (let j = i; j < s.length && j < i + maxSpan; j++) {
    const c = s[j]
    if (c === '\\') j++
    else if (c === '[') depth++
    else if (c === ']' && --depth === 0) {
      close = j
      break
    }
  }
  if (close < 0 || s[close + 1] !== '(') return null
  const m = /^\(\s*([^\s()]*(?:\([^\s()]*\)[^\s()]*)*)(?:\s+"[^"]*")?\s*\)/.exec(s.slice(close + 1, close + 1 + maxSpan))
  if (!m) return null
  return { label: s.slice(i + 1, close), href: m[1]!, end: close + 1 + m[0].length }
}
