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

test('commandLabel falls back to raw text', () => {
  assert.equal(commandLabel('not json'), 'not json')
  assert.equal(commandLabel('{"cmd":"nope"}'), '{"cmd":"nope"}')
  assert.equal(commandLabel('{"foo":1}'), '{"foo":1}')
  assert.equal(commandLabel(''), '')
  assert.equal(commandLabel(undefined), '')
})
