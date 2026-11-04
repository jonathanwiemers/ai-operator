// Tests of src/transcriptFold.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { foldName, foldTranscript } from '../src/transcriptFold.ts'

const step = { kind: 'step', stepNo: 3, step: 'run implementer', status: 'started' }
const says = { kind: 'says', text: 'Looking at the parser.' }
const agent = { kind: 'tool', tool: 'Agent', input: 'Find callers', toolUseID: 'toolu_2', subagent: 'Explore' }
const grep = { kind: 'tool', tool: 'Grep', input: 'consume', parent: 'toolu_2', model: 'claude-haiku-5-5', tokens: 5065 }
const subSays = { kind: 'says', text: 'Two callers.', parent: 'toolu_2', model: 'claude-haiku-5-5', tokens: 5144 }
const edit = { kind: 'tool', tool: 'Edit', input: 'engine.go' }

test('a run without subagents gives one row per entry', () => {
  const entries = [step, says, edit, { kind: 'tool', tool: 'Bash', input: 'go test' }]
  assert.deepEqual(
    foldTranscript(entries),
    entries.map((entry) => ({ kind: 'entry', entry })),
  )
})

test("a subagent's lines fold under the tool line that started it", () => {
  const rows = foldTranscript([step, says, agent, grep, subSays, edit])
  assert.equal(rows.length, 4)
  assert.deepEqual(rows[0], { kind: 'entry', entry: step })
  assert.deepEqual(rows[2], {
    kind: 'fold',
    tool: agent,
    children: [grep, subSays],
    model: 'claude-haiku-5-5',
    tokens: 10209,
    count: 2,
  })
  assert.deepEqual(rows[3], { kind: 'entry', entry: edit })
  assert.equal(foldName(rows[2]), 'Explore')
})

test('children that arrive later join their fold', () => {
  const entries = [agent]
  let rows = foldTranscript(entries)
  assert.deepEqual(rows, [{ kind: 'fold', tool: agent, children: [], model: '', tokens: 0, count: 0 }])
  // The main thread goes on while the subagent still works.
  entries.push(edit, grep)
  rows = foldTranscript(entries)
  assert.equal(rows.length, 2)
  assert.deepEqual(rows[0].children, [grep])
  entries.push(subSays)
  rows = foldTranscript(entries)
  assert.equal(rows[0].count, 2)
  assert.equal(rows[0].tokens, 10209)
})

test('two subagents fold apart, and models are listed once each', () => {
  const other = { ...agent, toolUseID: 'toolu_9', subagent: undefined, input: 'Read the logs' }
  const a = { kind: 'thinks', text: 'hmm', parent: 'toolu_9', model: 'claude-sonnet-5-5', tokens: 7 }
  const b = { kind: 'says', text: 'ok', parent: 'toolu_9', model: 'claude-haiku-5-5' }
  const c = { kind: 'says', text: 'done', parent: 'toolu_9', model: 'claude-sonnet-5-5', tokens: 3 }
  const rows = foldTranscript([agent, other, grep, a, b, c])
  assert.equal(rows.length, 2)
  assert.equal(rows[0].count, 1)
  assert.equal(rows[1].count, 3)
  assert.equal(rows[1].tokens, 10)
  assert.equal(rows[1].model, 'claude-sonnet-5-5, claude-haiku-5-5')
  assert.equal(foldName(rows[1]), 'Read the logs')
})

test('unknown and nested parents show unfolded', () => {
  const orphan = { kind: 'says', text: 'lost', parent: 'toolu_gone', tokens: 4 }
  // A subagent starting a subagent: its tool line joins the outer fold,
  // the inner lines show unfolded.
  const nested = { kind: 'tool', tool: 'Task', input: 'deeper', toolUseID: 'toolu_3', parent: 'toolu_2' }
  const inner = { kind: 'says', text: 'inner', parent: 'toolu_3' }
  const rows = foldTranscript([orphan, agent, nested, inner])
  assert.deepEqual(rows[0], { kind: 'entry', entry: orphan })
  assert.deepEqual(rows[1].children, [nested])
  assert.deepEqual(rows[2], { kind: 'entry', entry: inner })
})

test('a child before its tool line is not folded', () => {
  const rows = foldTranscript([grep, agent])
  assert.deepEqual(rows[0], { kind: 'entry', entry: grep })
  assert.equal(rows[1].count, 0)
})
