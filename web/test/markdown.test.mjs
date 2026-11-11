// Tests of src/markdown.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { renderInline, renderMarkdown } from '../src/markdown.ts'

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
  ['table with alignment, inline content and escaped pipe', '| # | Step | Result |\n|--:|:-:|---|\n| 1 | `a\\|b` | **ok** \\| x |\n| 2 | b |\n\nafter',
    '<div class="table-scroll"><table><thead><tr><th style="text-align:right;">#</th><th style="text-align:center;">Step</th><th>Result</th></tr></thead><tbody>' +
    '<tr><td style="text-align:right;">1</td><td style="text-align:center;"><code>a|b</code></td><td><strong>ok</strong> | x</td></tr>' +
    '<tr><td style="text-align:right;">2</td><td style="text-align:center;">b</td><td></td></tr></tbody></table></div><p>after</p>'],
  ['table without pipes around', 'a | b\n--- | ---', '<div class="table-scroll"><table><thead><tr><th>a</th><th>b</th></tr></thead></table></div>'],
  ['delimiter with another cell count is text', '| a | b |\n|---|', '<p>| a | b |\n|---|</p>'],
  ['HTML comments are dropped', '<!-- marker --> text <!-- x -->here\n\n<!-- alone -->', '<p> text here</p>'],
  ['unclosed comment is text', '<!-- open', '<p>&lt;!-- open</p>'],
  ['details with summary', '<details open><summary>Run <code>r</code></summary>\n\n- a\n\n</details>\nafter',
    '<details open><summary>Run <code>r</code></summary><ul><li><p>a</p></li></ul></details><p>after</p>'],
  ['nested details, text after the close', '<details><summary>o</summary>\n<details>\nx\n</details>\ny</details> z',
    '<details><summary>o</summary><details><p>x</p></details><p>y</p></details><p>z</p>'],
  ['details without a close run to the end', '<details>\na', '<details><p>a</p></details>'],
  ['inline tags without attributes', '<b>b</b> <i>i</i> <s>s</s> <kbd>*k*</kbd> H<sub>2</sub>O<sup>+</sup> a<br>b <BR/>c',
    '<p><strong>b</strong> <em>i</em> <del>s</del> <kbd>*k*</kbd> H<sub>2</sub>O<sup>+</sup> a<br>b <br>c</p>'],
  ['tags with attributes and other tags are text', '<code onclick="x">c</code> <a href="https://a.example">a</a> <span>s</span> <b>open',
    '<p>&lt;code onclick=&quot;x&quot;&gt;c&lt;/code&gt; &lt;a href=&quot;https://a.example&quot;&gt;a&lt;/a&gt; &lt;span&gt;s&lt;/span&gt; &lt;b&gt;open</p>'],
  ['entities', 'Hold&#39;em &amp; &lt;b&gt; &#x1F600; &#0; &mdash; &bogus; \\&amp; `&amp;`',
    '<p>Hold&#39;em &amp; &lt;b&gt; 😀 � — &amp;bogus; &amp;amp; <code>&amp;amp;</code></p>'],
  ['strikethrough', '~~gone~~ ~one~ ~~ spaced ~~', '<p><del>gone</del> ~one~ ~~ spaced ~~</p>'],
  ['rules', 'a\n---\n* * *\n- a\n- - -\n___', '<p>a</p><hr><hr><ul><li><p>a</p></li></ul><hr><hr>'],
  ['task list', '- [ ] todo\n- [x] done\n- [y] no', '<ul><li class="task"><input type="checkbox" disabled><p>todo</p></li>' +
    '<li class="task"><input type="checkbox" disabled checked><p>done</p></li><li><p>[y] no</p></li></ul>'],
  ['MR description footer', 'text\n\n<sub>Written by the agent.</sub>', '<p>text</p><p><sub>Written by the agent.</sub></p>'],
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
  await md('<b><!--~~&#'.repeat(16 * 1024))
  await md('<details>\n| a |\n'.repeat(8 * 1024))
  await md('| a | b |\n|---|---|\n' + '| x |\n'.repeat(16 * 1024))
  assert.ok(Date.now() - start < 5000, `took ${Date.now() - start} ms`)
})

test('a retrospective reads as on GitLab, its HTML as text', async () => {
  const src = [
    '🧭 **Retrospective**',
    '',
    '### Merged',
    '- #12 Add the `store` (!40)',
    '',
    '| Cost driver | USD | Tokens |',
    '|---|--:|--:|',
    '| #12 | $1.20 | 1.2M |',
    '',
    '1. Follow-up <img src=x onerror=alert(1)>',
  ].join('\n')
  assert.equal(
    await md(src),
    '<p>🧭 <strong>Retrospective</strong></p><h5>Merged</h5><ul><li><p>#12 Add the <code>store</code> (!40)</p></li></ul>' +
      '<div class="table-scroll"><table><thead><tr><th>Cost driver</th><th style="text-align:right;">USD</th><th style="text-align:right;">Tokens</th></tr></thead>' +
      '<tbody><tr><td>#12</td><td style="text-align:right;">$1.20</td><td style="text-align:right;">1.2M</td></tr></tbody></table></div>' +
      '<ol><li><p>Follow-up &lt;img src=x onerror=alert(1)&gt;</p></li></ol>',
  )
})

async function mdInline(src) {
  const html = await renderToString(createSSRApp({ render: () => h('span', renderInline(src)) }))
  return html.replace(/^<span>|<\/span>$/g, '').replaceAll('<!--[-->', '').replaceAll('<!--]-->', '')
}

const inlineCases = [
  ['strong, emphasis and code', '**bold** _em_ `code`', '<strong>bold</strong> <em>em</em> <code>code</code>'],
  ['a link is its label', 'see [the *MR*](https://a.example/mr)', 'see the <em>MR</em>'],
  ['javascript link is its label', '[click](javascript:alert(1))', 'click'],
  ['list markers dropped', '- a\n- b', 'a b'],
  ['ordered and task items', '1. [x] done\n2. [ ] open', 'done open'],
  ['heading keeps its text', '# Title', 'Title'],
  ['quote markers dropped', '> > quoted **x**', 'quoted <strong>x</strong>'],
  ['fences and rules keep the text only', '```js\nlet a\n```\n---\nafter', 'let a after'],
  ['details tags dropped', '<details><summary>More</summary>\nbody\n</details>', 'More body'],
  ['newlines fold into spaces', 'one\ntwo\r\n\nthree', 'one two three'],
  ['emphasis across lines', '**a\nb**', '<strong>a b</strong>'],
  ['HTML tags are text', '<script>alert(1)</script> <a href="https://x">y</a>', '&lt;script&gt;alert(1)&lt;/script&gt; &lt;a href=&quot;https://x&quot;&gt;y&lt;/a&gt;'],
]

for (const [name, src, want] of inlineCases) {
  test(`inline: ${name}`, async () => {
    assert.equal(await mdInline(src), want)
  })
}

test('inline: no href anywhere in the output', async () => {
  const out = await mdInline('[a](https://a.example) and [b](http://b.example "t") <https://c.example>')
  assert.doesNotMatch(out, /href/)
  assert.equal(out, 'a and b &lt;https://c.example&gt;')
})

test('inline: links inside a row render no anchor', async () => {
  const html = await renderToString(
    createSSRApp({ render: () => h('a', { href: '/tasks/1' }, [h('span', renderInline('see [MR](https://x)'))]) }),
  )
  assert.equal((html.match(/<a\b/g) ?? []).length, 1)
  assert.match(html, /see MR/)
})
