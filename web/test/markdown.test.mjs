// Tests of src/markdown.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { renderMarkdown } from '../src/markdown.ts'

const link = (href, label) => h('a', { href, rel: 'noopener noreferrer nofollow' }, label)

async function md(src) {
  const html = await renderToString(createSSRApp({ render: () => h('div', renderMarkdown(src, link)) }))
  // Drop the div and the fragment comments of SSR.
  return html.replace(/^<div>|<\/div>$/g, '').replaceAll('<!--[-->', '').replaceAll('<!--]-->', '')
}

const cases = [
  ['paragraphs', 'one\ntwo\n\nthree', '<p>one\ntwo</p><p>three</p>'],
  ['headings', '# a\n## b\n### c\n#### d', '<h3>a</h3><h4>b</h4><h5>c</h5><h5>d</h5>'],
  ['no heading without a space', '#tag', '<p>#tag</p>'],
  ['emphasis and strong', '*a* _b_ **c** __d__ ***e***', '<p><em>a</em> <em>b</em> <strong>c</strong> <strong>d</strong> <strong><em>e</em></strong></p>'],
  ['strong inside emphasis', '*a **b** c*', '<p><em>a <strong>b</strong> c</em></p>'],
  ['no emphasis inside words with _', 'snake_case_name', '<p>snake_case_name</p>'],
  ['unclosed delimiters', '2 * 3 * 4 and **open', '<p>2 * 3 * 4 and **open</p>'],
  ['escapes', '\\*not\\* \\[x\\](https://a.example)', '<p>*not* [x](https://a.example)</p>'],
  ['code span', 'use `a *b* <c>` here', '<p>use <code>a *b* &lt;c&gt;</code> here</p>'],
  ['fenced code', '```js\nif (a < b) **x**\n```\nafter', '<pre><code>if (a &lt; b) **x**</code></pre><p>after</p>'],
  ['unordered list', '- a\n- b\n  more\n* c', '<ul><li><p>a</p></li><li><p>b\nmore</p></li></ul><ul><li><p>c</p></li></ul>'],
  ['ordered list with start', '3. a\n4. b', '<ol start="3"><li><p>a</p></li><li><p>b</p></li></ol>'],
  ['nested list', '- a\n  - b\n- c', '<ul><li><p>a</p><ul><li><p>b</p></li></ul></li><li><p>c</p></li></ul>'],
  ['loose list', '- a\n\n- b\n\nafter', '<ul><li><p>a</p></li><li><p>b</p></li></ul><p>after</p>'],
  ['blockquote', '> a\n> > b\n\nc', '<blockquote><p>a</p><blockquote><p>b</p></blockquote></blockquote><p>c</p>'],
  ['http link', '[the *news*](https://a.example/x?y=1&z=2)', '<p><a href="https://a.example/x?y=1&amp;z=2" rel="noopener noreferrer nofollow">the <em>news</em></a></p>'],
  ['link with a title and parentheses', '[w](https://a.example/a_(b) "t")', '<p><a href="https://a.example/a_(b)" rel="noopener noreferrer nofollow">w</a></p>'],
  ['javascript link is text', '[click](javascript:alert(1))', '<p>click</p>'],
  ['relative link is text', '[x](/admin)', '<p>x</p>'],
  ['no nested links', '[a [b](https://b.example)](https://a.example)', '<p><a href="https://a.example/" rel="noopener noreferrer nofollow">a [b](https://b.example)</a></p>'],
  ['raw HTML is text', '<script>alert(1)</script>\n<img src=x onerror=alert(1)>', '<p>&lt;script&gt;alert(1)&lt;/script&gt;\n&lt;img src=x onerror=alert(1)&gt;</p>'],
  ['images are text', '![x](https://a.example/i.png)', '<p>!<a href="https://a.example/i.png" rel="noopener noreferrer nofollow">x</a></p>'],
  ['tables are text', '| a | b |\n|---|---|', '<p>| a | b |\n|---|---|</p>'],
]

for (const [name, src, want] of cases) {
  test(name, async () => assert.equal(await md(src), want))
}

test('deep nesting stays text and terminates', async () => {
  const html = await md('>'.repeat(5000) + ' x\n' + '- '.repeat(5000) + 'y\n' + '*'.repeat(5000) + 'z' + '*'.repeat(5000))
  assert.ok(html.includes('x') && html.includes('y') && html.includes('z'))
})

test('64 KiB of hostile brackets and delimiters renders quickly', async () => {
  const start = Date.now()
  await md('[*_`'.repeat(16 * 1024))
  await md('- \n\n'.repeat(16 * 1024))
  assert.ok(Date.now() - start < 5000, `took ${Date.now() - start} ms`)
})
