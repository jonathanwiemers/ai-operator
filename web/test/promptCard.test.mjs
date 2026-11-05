// Tests of src/promptCard.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  answerCommand,
  canAnswerText,
  maxAnswerText,
  promptAnchor,
  promptOptions,
  promptStatus,
  readOnlyText,
  recommendedOption,
  splitPrompts,
  textBox,
} from '../src/promptCard.ts'

const option = (id, label, extra = {}) => ({ id, label, actions: [], recommended: false, fromAnswer: false, ...extra })

const prompt = (extra = {}) => ({
  id: 'Q4',
  kind: 'proposal',
  question: 'Re-review #42?',
  options: [
    option('A', 'Do nothing'),
    option('B', 'Re-review #42', { recommended: true, detail: 'its reviewers failed', actions: ['reReview #42'] }),
    option('C', 'Skip #42'),
  ],
  allowFreeText: true,
  state: 'open',
  canAnswer: true,
  target: { namespace: 'team-a', task: 'shop-pilot-m3' },
  postedAt: '2026-11-05T09:00:00Z',
  ...extra,
})

test('promptOptions: the recommended option first, the others in order', () => {
  const lines = promptOptions(prompt())
  assert.deepEqual(
    lines.map((o) => o.id),
    ['B', 'A', 'C'],
  )
  assert.deepEqual(lines[0].tags, ['recommended'])
  assert.equal(lines[0].detail, 'its reviewers failed')
  assert.deepEqual(lines[0].actions, ['reReview #42'])
  assert.deepEqual(lines[1].tags, [])
  assert.ok(lines.every((o) => !o.chosen))
})

test('promptOptions: an option from the answer follows and is tagged', () => {
  const p = prompt()
  p.options.push(option('D', 'From your answer: retry #42', { fromAnswer: true, actions: ['retry #42: a new run'] }))
  const lines = promptOptions(p)
  assert.deepEqual(
    lines.map((o) => o.id),
    ['B', 'A', 'C', 'D'],
  )
  assert.deepEqual(lines[3].tags, ['from your answer'])
})

test('promptOptions: marks the chosen option, tolerates missing actions', () => {
  const p = prompt({ state: 'answered', answer: { optionID: 'C', by: 'ada', at: '2026-11-05T10:00:00Z' } })
  delete p.options[1].actions
  const lines = promptOptions(p)
  assert.deepEqual(
    lines.filter((o) => o.chosen).map((o) => o.id),
    ['C'],
  )
  assert.deepEqual(lines[0].actions, [])
  assert.deepEqual(promptOptions(prompt({ options: undefined })), [])
})

test('recommendedOption: the compact card button', () => {
  assert.equal(recommendedOption(prompt()).id, 'B')
  assert.equal(recommendedOption(prompt({ options: [option('A', 'Yes')] })), undefined)
})

test('promptStatus: open waits, with the posting time', () => {
  assert.deepEqual(promptStatus(prompt()), { tone: 'warning', text: 'Waiting for an answer', at: '2026-11-05T09:00:00Z' })
  assert.equal(promptStatus(prompt({ state: undefined })).text, 'Waiting for an answer')
})

test('promptStatus: answered names who, the option, where and when', () => {
  const at = '2026-11-05T10:00:00Z'
  assert.deepEqual(promptStatus(prompt({ state: 'answered', answer: { optionID: 'B', by: 'ada', at, via: 'desk' } })), {
    tone: 'success',
    text: 'Answered by ada in the Desk: Re-review #42',
    at,
  })
  assert.equal(
    promptStatus(prompt({ state: 'answered', answer: { optionID: 'A', by: 'bob', at, via: 'gitlab' } })).text,
    'Answered by bob on GitLab: Do nothing',
  )
  assert.equal(
    promptStatus(prompt({ state: 'answered', answer: { optionID: 'E', by: 'ada', at } })).text,
    'Answered by ada: option E',
  )
  const long = 'use\n the  v2 API '.padEnd(300, 'x')
  const text = promptStatus(prompt({ kind: 'needsInput', state: 'answered', answer: { text: long, by: 'ada', at, via: 'desk' } })).text
  assert.ok(text.startsWith('Answered by ada in the Desk: "use the v2 API xx'), text)
  assert.ok(text.endsWith('…"'), text)
  assert.deepEqual(promptStatus(prompt({ state: 'answered', closedAt: at })), { tone: 'success', text: 'Answered', at })
})

test('promptStatus: superseded and expired say why they closed', () => {
  const closedAt = '2026-11-05T11:00:00Z'
  assert.deepEqual(promptStatus(prompt({ state: 'superseded', closedAt })), {
    tone: 'neutral',
    text: 'Superseded: its cause resolved or a newer question replaced it',
    at: closedAt,
  })
  assert.deepEqual(promptStatus(prompt({ state: 'expired', closedAt })), {
    tone: 'neutral',
    text: 'Expired: nobody answered in time',
    at: closedAt,
  })
})

test('answerCommand: an option sends the prompt and option id', () => {
  assert.deepEqual(answerCommand(prompt(), { option: 'B' }), {
    ok: true,
    cmd: { cmd: 'answer', prompt: 'Q4', option: 'B' },
    label: 'Re-review #42',
  })
  assert.deepEqual(answerCommand(prompt(), { option: 'E' }), { ok: false, error: 'Q4 has no option E.' })
})

test('answerCommand: text is trimmed and sent as the answer', () => {
  assert.deepEqual(answerCommand(prompt(), { text: '  retry it instead \n' }), {
    ok: true,
    cmd: { cmd: 'answer', prompt: 'Q4', text: 'retry it instead' },
    label: 'Your answer',
  })
})

test('answerCommand: empty, oversize and refused text', () => {
  assert.deepEqual(answerCommand(prompt(), { text: ' \n\t' }), { ok: false, error: 'Write an answer first.' })
  const exact = 'x'.repeat(maxAnswerText)
  assert.equal(answerCommand(prompt(), { text: exact }).ok, true)
  assert.deepEqual(answerCommand(prompt(), { text: exact + 'x' }), {
    ok: false,
    error: 'The answer is longer than 4000 bytes.',
  })
  // Bytes, as the operator counts: 2000 two-byte characters fit, one more does not.
  assert.equal(answerCommand(prompt(), { text: 'ä'.repeat(2000) }).ok, true)
  assert.equal(answerCommand(prompt(), { text: 'ä'.repeat(2001) }).ok, false)
  assert.deepEqual(answerCommand(prompt({ allowFreeText: false }), { text: 'hi' }), {
    ok: false,
    error: 'Q4 takes no free text; pick an option.',
  })
})

test('answerCommand: closed prompts and non-owners send nothing', () => {
  assert.deepEqual(answerCommand(prompt({ state: 'answered' }), { option: 'B' }), {
    ok: false,
    error: 'Q4 is closed (answered).',
  })
  assert.deepEqual(answerCommand(prompt({ canAnswer: false }), { option: 'B' }), {
    ok: false,
    error: "Only the milestone's owners can answer.",
  })
  assert.equal(answerCommand(prompt({ canAnswer: false }), { text: 'hi' }).ok, false)
})

test('read-only rule: non-owners see the prompt without buttons or text box', () => {
  assert.equal(readOnlyText(prompt()), '')
  assert.equal(readOnlyText(prompt({ canAnswer: false })), "Only the milestone's owners can answer.")
  assert.equal(readOnlyText(prompt({ canAnswer: false, state: 'expired' })), '')
  assert.equal(canAnswerText(prompt()), true)
  assert.equal(canAnswerText(prompt({ canAnswer: false })), false)
  assert.equal(canAnswerText(prompt({ allowFreeText: false })), false)
  assert.equal(canAnswerText(prompt({ state: 'superseded' })), false)
})

test('textBox: action prompts ask to do it differently, agent questions for the answer', () => {
  assert.equal(textBox(prompt()).placeholder, 'Do it differently: …')
  assert.match(textBox(prompt()).hint, /option for you to confirm/)
  assert.equal(textBox(prompt({ kind: 'needsInput' })).placeholder, 'Your answer…')
})

test('splitPrompts: open first, then closed, each in view order', () => {
  const list = [
    prompt({ id: 'Q9', state: 'expired' }),
    prompt({ id: 'Q8' }),
    prompt({ id: 'Q7', state: 'answered' }),
    prompt({ id: 'Q6', state: undefined }),
  ]
  const { open, closed } = splitPrompts(list)
  assert.deepEqual(
    open.map((p) => p.id),
    ['Q8', 'Q6'],
  )
  assert.deepEqual(
    closed.map((p) => p.id),
    ['Q9', 'Q7'],
  )
  assert.deepEqual(splitPrompts(undefined), { open: [], closed: [] })
})

test('promptAnchor: the id GitLab links scroll to', () => {
  assert.equal(promptAnchor('Q4'), 'prompt-Q4')
})

test('taskPrompts keeps open needsInput prompts and the newest closed one', async () => {
  const { taskPrompts } = await import('../src/promptCard.ts')
  const p = (id, kind, state) => ({ id, kind, state })
  const got = taskPrompts([
    p('Q5', 'needsInput', 'answered'),
    p('Q4', 'needsInput', 'open'),
    p('Q3', 'mainHealth', 'open'),
    p('Q2', 'needsInput', 'superseded'),
  ])
  assert.deepEqual(got.map((x) => x.id), ['Q4', 'Q5'])
  assert.deepEqual(taskPrompts(undefined), [])
})
