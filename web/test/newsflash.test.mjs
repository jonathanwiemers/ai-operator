// Tests of src/newsflash.ts: npm test.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { newsflash } from '../src/newsflash.ts'

const item = (title, topic, importance = 2, publishedAt) => ({ title, topic, importance, publishedAt, url: `https://a.example/${title}` })

test('groups by the configured topics, then the others alphabetically, then items without topic', () => {
  const blocks = [
    { type: 'heading', text: 'Today' },
    { type: 'items', items: [item('a', 'Zoo'), item('b', 'world'), item('c'), item('d', 'Arts')] },
    { type: 'markdown', text: 'end' },
    { type: 'items', items: [item('e', 'Tech'), item('f', ' World ')] },
  ]
  const { groups, rest } = newsflash(blocks, ['Tech', 'World', 'Sports'])
  assert.deepEqual(
    groups.map((g) => [g.topic, g.items.map((i) => i.title)]),
    [
      ['Tech', ['e']],
      ['World', ['b', 'f']],
      ['Arts', ['d']],
      ['Zoo', ['a']],
      ['', ['c']],
    ],
  )
  assert.deepEqual(
    rest.map((b) => b.type),
    ['heading', 'markdown'],
  )
})

test('orders a topic by importance, then newest, unknown times last', () => {
  const { groups } = newsflash(
    [
      {
        type: 'items',
        items: [
          item('old', 't', 2, '2026-10-27T07:00:00Z'),
          item('undated', 't', 2),
          item('minor', 't', 1, '2026-10-29T07:00:00Z'),
          item('new', 't', 2, '2026-10-29T06:00:00Z'),
          item('top', 't', 3, '2026-10-20T07:00:00Z'),
        ],
      },
    ],
    [],
  )
  assert.deepEqual(
    groups[0].items.map((i) => i.title),
    ['top', 'new', 'old', 'undated', 'minor'],
  )
})
