// Tests of src/problemFacts.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { factRows, hidesReason, problemCause } from '../src/problemFacts.ts'

const project = { namespace: 'team-a', name: 'ops' }
const now = Date.parse('2026-11-18T12:00:00Z')

// incident: #558's facts after run 1 (the replay of %29).
const incident = {
  iid: 558,
  issue: { iid: 558, title: 'Desk facts', url: 'https://gitlab.example/ops/-/issues/558' },
  mergeRequest: {
    iid: 583,
    url: 'https://gitlab.example/ops/-/merge_requests/583',
    headSHA: 'abc1234def5678',
    commitsAhead: 3,
    pushedAt: '2026-11-18T11:55:00Z',
  },
  pipeline: {
    id: 901,
    url: 'https://gitlab.example/ops/-/pipelines/901',
    status: 'failed',
    failedJobs: [{ name: 'test', url: 'https://gitlab.example/ops/-/pipelines/901/failures' }],
  },
  run: {
    task: 'ops-m29-558',
    number: 1,
    result: 'Failed',
    error: 'decoding implementer answer: no JSON object in agent result',
    cause: 'the run failed: decoding implementer answer: no JSON object in agent result',
    noteURL: 'https://gitlab.example/ops/-/issues/558#note_1',
  },
  saved: { sha: 'abc1234def5678', lines: 348 },
  blocked: [559, 560],
}

const texts = (rows) => rows.map((r) => [r.key, r.parts.map((p) => p.text).join(' · ')])

test('factRows: full facts give one row per fact, with links', () => {
  const rows = factRows(incident, project, now)
  assert.deepEqual(texts(rows), [
    ['issue', '#558'],
    ['mr', '!583 · abc1234d · 3 commits ahead · pushed 5 minutes ago'],
    ['pipeline', 'pipeline #901 failed · test'],
    ['run', 'run 1 failed: decoding implementer answer: no JSON object in agent result · failure note · log'],
    ['saved', '348 lines saved as WIP commit abc1234d'],
    ['blocked', '#558 → #559 → #560'],
  ])
  assert.equal(rows[0].parts[0].href, incident.issue.url)
  assert.equal(rows[1].parts[0].href, incident.mergeRequest.url)
  assert.equal(rows[2].parts[1].href, 'https://gitlab.example/ops/-/pipelines/901/failures')
  const run = rows[3].parts
  assert.equal(run[1].href, incident.run.noteURL)
  assert.deepEqual(run[2].to, { name: 'task', params: { namespace: 'team-a', name: 'ops', task: 'ops-m29-558' }, query: { tab: 'logs' } })
})

test('factRows: no MR, no pipeline and no run leave no rows', () => {
  const rows = factRows({ iid: 7, issue: { iid: 7, title: 't', url: 'https://gitlab.example/i/7' } }, project, now)
  assert.deepEqual(texts(rows), [['issue', '#7']])
})

test('factRows: an MR without commits says 0 commits ahead; a run without error has no colon', () => {
  const rows = factRows(
    { ...incident, mergeRequest: { iid: 583, url: incident.mergeRequest.url }, pipeline: undefined, saved: undefined, blocked: undefined, run: { task: 't', number: 2, result: 'NeedsInput' } },
    project,
    now,
  )
  assert.deepEqual(texts(rows), [
    ['issue', '#558'],
    ['mr', '!583 · 0 commits ahead'],
    ['run', 'run 2 needs input · log'],
  ])
})

test('factRows: uncommitted-only saved work', () => {
  const rows = factRows({ ...incident, saved: { uncommittedLines: 1 } }, project, now)
  assert.deepEqual(texts(rows).find((r) => r[0] === 'saved'), ['saved', '1 line left uncommitted'])
  const both = factRows({ ...incident, saved: { sha: 'abc1234def', lines: 348, uncommittedLines: 12 } }, project, now)
  assert.deepEqual(texts(both).find((r) => r[0] === 'saved'), ['saved', '348 lines saved as WIP commit abc1234d · 12 lines left uncommitted'])
})

test('factRows: a single blocked issue is a chain of two', () => {
  const rows = factRows({ ...incident, blocked: [559] }, project, now)
  assert.deepEqual(texts(rows).at(-1), ['blocked', '#558 → #559'])
})

test('problemCause: the run cause replaces the generic needs-input reason only', () => {
  const cause = problemCause(incident)
  assert.equal(cause, incident.run.cause)
  assert.equal(hidesReason(cause, 'needsInput'), true)
  // A stuck or budget reason says something else: it stays.
  assert.equal(hidesReason(cause, 'stuck'), false)
  assert.equal(hidesReason(cause, 'budget'), false)
})

test('problemCause: without a cause the pause cards keep their reasons (the fallback)', () => {
  assert.equal(problemCause(undefined), undefined)
  const old = problemCause({ ...incident, run: { ...incident.run, cause: undefined } })
  assert.equal(old, undefined)
  assert.equal(hidesReason(old, 'needsInput'), false)
})
