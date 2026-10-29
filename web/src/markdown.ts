import { h, type VNode, type VNodeChild } from 'vue'
import { safeURL } from './format.ts'

// A markdown subset rendered straight to VNodes: agent text never becomes an
// HTML string, so whatever it contains shows as text. Supported are
// paragraphs, headings (as h3–h5), lists, blockquotes, emphasis, strong,
// code spans, fenced code and links to http(s) URLs; everything else is
// text. The server already refused raw HTML, images and other links.

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

export function renderMarkdown(src: string, link: LinkFn): VNode[] {
  return blocks(src.replace(/\r\n?/g, '\n').split('\n'), link, 0)
}

function startsBlock(line: string): boolean {
  return fenceRe.test(line) || headingRe.test(line) || quoteRe.test(line) || itemRe.test(line)
}

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
    for (i++; i < lines.length && lines[i]!.trim() !== '' && !startsBlock(lines[i]!); i++) para.push(lines[i]!.trim())
    out.push(h('p', inline(para.join('\n'), link, 0)))
  }
  return out
}

// list renders the list that starts at lines[start] into out and returns the
// index of the first line after it. An item's further lines are indented to
// its text or continue its paragraph.
function list(lines: string[], start: number, link: LinkFn, depth: number, out: VNode[]): number {
  // Items of one list share their bullet, or the delimiter after their number.
  const kind = (m: RegExpExecArray | null) => m && (/\d/.test(m[2]!) ? m[2]!.slice(-1) : m[2]!)
  const first = itemRe.exec(lines[start]!)!
  const items: string[][] = []
  let width = 0
  let i = start
  while (i < lines.length) {
    const line = lines[i]!
    const m = itemRe.exec(line)
    if (m && kind(m) === kind(first) && m[1]!.length < width + (items.length ? 0 : 4)) {
      width = m[1]!.length + m[2]!.length + Math.min(Math.max(m[3]!.length, 1), 4)
      items.push([m[4]!])
      i++
      continue
    }
    const indent = line.length - line.trimStart().length
    const cur = items[items.length - 1]!
    if (line.trim() === '') {
      // Blank lines end the list unless an item goes on after them.
      let j = i + 1
      while (j < lines.length && lines[j]!.trim() === '') j++
      const next = lines[j]
      if (next === undefined) break
      if (next.length - next.trimStart().length < width && kind(itemRe.exec(next)) !== kind(first)) break
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
  const children = items.map((it) => h('li', blocks(it, link, depth + 1)))
  const n = Number.parseInt(first[2]!, 10)
  out.push(/\d/.test(first[2]!) ? h('ol', n !== 1 ? { start: n } : null, children) : h('ul', children))
  return i
}

const punct = /[!-/:-@[-`{-~]/

// inline renders emphasis, strong, code spans and links; the rest is text.
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
      const end = closingRun(s, i + n, n)
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

// closingRun returns the index of the first run of exactly n backticks at or
// after from (within maxSpan), or -1.
function closingRun(s: string, from: number, n: number): number {
  for (let j = from; j < s.length && j < from + maxSpan; ) {
    if (s[j] !== '`') {
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
