import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  agentTurnPending,
  chatWaiting,
  commandLabel,
} from '../src/chatState.ts'

test('chatWaiting: capacity uses the task status message', () => {
  const w = chatWaiting({
    pending: true,
    delivered: false,
    phase: 'Queued',
    message: 'waiting for capacity (1/1 runs in flight)',
  })
  assert.equal(w?.kind, 'capacity')
  assert.equal(w?.label, 'waiting for capacity (1/1 runs in flight)')
  assert.equal(w?.canInterrupt, false)

  assert.equal(
    chatWaiting({ pending: true, delivered: false, phase: 'Pending', message: 'starting' })?.label,
    'starting',
  )
  assert.equal(
    chatWaiting({ pending: true, delivered: true, phase: 'Blocked', message: 'safety limit' })?.label,
    'safety limit',
  )
  assert.equal(
    chatWaiting({ pending: true, delivered: false, phase: 'Queued' })?.label,
    'waiting for capacity',
  )
})

test('chatWaiting: starting without a pod', () => {
  const hib = chatWaiting({
    pending: true,
    delivered: true,
    phase: 'Hibernated',
    message: 'hibernated',
  })
  assert.equal(hib?.kind, 'starting')
  assert.equal(hib?.label, 'starting the agent…')
  assert.equal(hib?.canInterrupt, false)

  const runningNoPod = chatWaiting({
    pending: true,
    delivered: true,
    phase: 'Running',
    message: 'answering chat',
    podName: '',
  })
  assert.equal(runningNoPod?.kind, 'starting')
  assert.equal(runningNoPod?.canInterrupt, false)
})

test('chatWaiting: answering only Running/Idle with a pod', () => {
  const ans = chatWaiting({
    pending: true,
    delivered: true,
    phase: 'Running',
    message: 'answering chat',
    podName: 'task-pod',
  })
  assert.equal(ans?.kind, 'answering')
  assert.equal(ans?.label, 'the agent is answering…')
  assert.equal(ans?.canInterrupt, true)

  const idle = chatWaiting({
    pending: true,
    delivered: true,
    phase: 'Idle',
    podName: 'task-pod',
  })
  assert.equal(idle?.kind, 'answering')
  assert.equal(idle?.canInterrupt, true)

  const queuedDelivered = chatWaiting({
    pending: true,
    delivered: false,
    phase: 'Running',
    podName: 'task-pod',
  })
  assert.equal(queuedDelivered?.canInterrupt, false)
})

test('chatWaiting: null when not pending', () => {
  assert.equal(chatWaiting({ pending: false, delivered: false, phase: 'Running', podName: 'p' }), null)
})

test('agentTurnPending ignores commands', () => {
  assert.equal(agentTurnPending('message', 'delivered', false), true)
  assert.equal(agentTurnPending('interrupt', 'queued', false), true)
  assert.equal(agentTurnPending('command', 'delivered', false), false)
  assert.equal(agentTurnPending('message', 'answered', false), false)
  assert.equal(agentTurnPending('message', 'delivered', true), false)
})

test('commandLabel formats known commands', () => {
  assert.equal(commandLabel('{"cmd":"retry"}'), 'retry')
  assert.equal(commandLabel('{"cmd":"review"}'), 'review again')
  assert.equal(commandLabel('{"cmd":"pause"}'), 'pause')
  assert.equal(commandLabel('{"cmd":"resume"}'), 'resume')
  assert.equal(commandLabel('{"cmd":"use","default":true}'), 'use default')
  assert.equal(
    commandLabel('{"cmd":"use","engine":"claude","model":"opus-x","effort":"high"}'),
    'use claude opus-x high',
  )
  assert.equal(commandLabel('{"cmd":"use","engine":"cursor","model":"auto"}'), 'use cursor auto')
  assert.equal(commandLabel('{"cmd":"effort","effort":"high"}'), 'effort high')
  assert.equal(commandLabel('{"cmd":"effort","effort":"default"}'), 'effort default')
  assert.equal(commandLabel('{"cmd":"skip","issue":12}'), 'skip #12')
  assert.equal(commandLabel('{"cmd":"accept","proposal":3}'), 'accept P3')
  assert.equal(commandLabel('{"cmd":"reject","proposal":3}'), 'reject P3')
})

test('commandLabel: merge names the issue and the short head', () => {
  assert.equal(
    commandLabel('{"cmd":"merge","issue":12,"sha":"abcdef0123456789"}'),
    'merge #12 abcdef0',
  )
  for (const body of [
    '{"cmd":"merge","issue":12}',
    '{"cmd":"merge","issue":12,"sha":""}',
    '{"cmd":"merge","issue":0,"sha":"abcdef0"}',
    '{"cmd":"merge","issue":"12","sha":"abcdef0"}',
    '{"cmd":"merge","sha":"abcdef0"}',
  ]) {
    assert.equal(commandLabel(body), body)
  }
})

test('commandLabel: revisit names the decision and the text', () => {
  assert.equal(commandLabel('{"cmd":"revisit","decision":"d12-3"}'), 'revisit D12-3')
  assert.equal(
    commandLabel('{"cmd":"revisit","decision":"D12-3","text":"use  gRPC\\ninstead"}'),
    'revisit D12-3: "use gRPC instead"',
  )
  assert.equal(
    commandLabel(`{"cmd":"revisit","decision":"D12-3","text":"${'x'.repeat(41)}"}`),
    `revisit D12-3: "${'x'.repeat(39)}…"`,
  )
  assert.equal(commandLabel('{"cmd":"revisit","decision":"D12-3","text":" "}'), 'revisit D12-3')
  assert.equal(commandLabel('{"cmd":"revisit"}'), '{"cmd":"revisit"}')
})

test('commandLabel: answer names the prompt and the option or text', () => {
  assert.equal(commandLabel('{"cmd":"answer","prompt":"Q3","option":"A"}'), 'answer Q3: A')
  assert.equal(
    commandLabel('{"cmd":"answer","prompt":"Q3","text":"use the  second\\nAPI"}'),
    'answer Q3: "use the second API"',
  )
  const long = 'x'.repeat(41)
  assert.equal(
    commandLabel(`{"cmd":"answer","prompt":"Q3","text":"${long}"}`),
    `answer Q3: "${'x'.repeat(39)}…"`,
  )
  assert.equal(
    commandLabel(`{"cmd":"answer","prompt":"Q3","text":"${'x'.repeat(40)}"}`),
    `answer Q3: "${'x'.repeat(40)}"`,
  )
  for (const body of [
    '{"cmd":"answer","option":"A"}',
    '{"cmd":"answer","prompt":"Q3"}',
    '{"cmd":"answer","prompt":"Q3","text":"  "}',
    '{"cmd":"answer","prompt":3,"option":"A"}',
  ]) {
    assert.equal(commandLabel(body), body)
  }
})

test('commandLabel falls back to raw text', () => {
  assert.equal(commandLabel('not json'), 'not json')
  assert.equal(commandLabel('{"cmd":"nope"}'), '{"cmd":"nope"}')
  assert.equal(commandLabel('{"foo":1}'), '{"foo":1}')
  assert.equal(commandLabel(''), '')
  assert.equal(commandLabel(undefined), '')
})

test('commandLabel: draft names the follow-ups', () => {
  assert.equal(commandLabel('{"cmd":"draft","followUps":[1,3]}'), 'draft follow-ups 1, 3')
  assert.equal(commandLabel('{"cmd":"draft","followUps":[]}'), '{"cmd":"draft","followUps":[]}')
})
