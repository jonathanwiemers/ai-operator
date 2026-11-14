// Tests of src/finishing.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { companionURL, holdLines } from '../src/finishing.ts'

const entry = (over = {}) => ({ iid: 7, title: 'M7', task: 'shop-spec-m7', replanning: false, retroWriting: false, followUps: false, openIssues: [], companions: [], ...over })
const gl = 'https://gitlab.com/'

test('holdLines: nothing holds it, it closes on the next poll', () => {
  assert.deepEqual(holdLines(entry(), gl), [{ text: 'Closes on the next poll' }])
})

test('holdLines: one line per hold, in order', () => {
  const e = entry({
    replanning: true,
    retroWriting: true,
    followUps: true,
    openIssues: [{ iid: 42, title: 'Fix it', webURL: 'https://gitlab.com/g/p/-/issues/42' }, { iid: 43, webURL: 'https://gitlab.com/g/p/-/issues/43' }],
    companions: ['group/lib!7'],
  })
  assert.deepEqual(holdLines(e, gl), [
    { text: 'Re-planning' },
    { text: 'Retrospective being written' },
    { text: 'Follow-ups to settle', retro: true },
    { text: 'Open: #42 Fix it', href: 'https://gitlab.com/g/p/-/issues/42' },
    { text: 'Open: #43', href: 'https://gitlab.com/g/p/-/issues/43' },
    { text: 'Companions not landed:', links: [{ text: 'group/lib!7', href: 'https://gitlab.com/group/lib/-/merge_requests/7' }] },
  ])
})

test('companionURL: the merge request, the project without one', () => {
  assert.equal(companionURL('https://gl.example', 'a/b!3'), 'https://gl.example/a/b/-/merge_requests/3')
  assert.equal(companionURL('https://gl.example/', 'a/b'), 'https://gl.example/a/b')
})
