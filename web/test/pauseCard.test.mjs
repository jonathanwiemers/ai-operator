// Tests of src/pauseCard.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  pauseActions,
  pauseCommand,
  pauseEndsBy,
  pausePrompt,
  pauseTitle,
  reviewLimitLines,
  commandAnswer,
  deskCommand,
  wayOut,
  wayOutLine,
} from '../src/pauseCard.ts'

const ids = (kind) => pauseActions(kind).map((a) => a.id)
const targets = (kind) => pauseActions(kind).map((a) => a.target)

const allKinds = [
  'owner',
  'cycle',
  'stuck',
  'needsInput',
  'runFailed',
  'findings',
  'reviewErrors',
  'notReviewed',
  'ciRed',
  'ciCanceled',
  'mergeRequest',
  'mainRed',
  'config',
  'budget',
]

test('pauseActions: reviewer kinds offer review, merge anyway and skip', () => {
  for (const kind of ['reviewErrors', 'notReviewed']) {
    assert.deepEqual(ids(kind), ['review', 'merge', 'skip'], kind)
    assert.deepEqual(targets(kind), ['implement', 'spec', 'spec'], kind)
    const merge = pauseActions(kind)[1]
    assert.equal(merge.label, 'Merge anyway')
    assert.equal(merge.confirm, true)
    assert.equal(merge.ownerOnly, true)
    assert.equal(pauseActions(kind)[0].label, 'Review again')
  }
})

test('pauseActions: run kinds offer retry, the chat and skip', () => {
  for (const kind of ['findings', 'runFailed', 'needsInput']) {
    assert.deepEqual(ids(kind), ['retry', 'openChat', 'skip'], kind)
    assert.deepEqual(targets(kind), ['implement', 'link', 'spec'], kind)
  }
  assert.equal(pauseActions('findings', 12)[1].label, 'Open #12 chat')
  assert.equal(pauseActions('findings')[1].label, 'Open chat')
})

test('pauseActions: CI kinds offer retry, the pipeline and skip', () => {
  for (const kind of ['ciRed', 'ciCanceled']) {
    assert.deepEqual(ids(kind), ['retry', 'openPipeline', 'skip'], kind)
    assert.deepEqual(targets(kind), ['implement', 'link', 'spec'], kind)
  }
})

test('pauseActions: mergeRequest offers the MR, resume and skip', () => {
  assert.deepEqual(ids('mergeRequest'), ['openMR', 'resume', 'skip'])
  assert.deepEqual(targets('mergeRequest'), ['link', 'spec', 'spec'])
})

test('pauseActions: plan kinds offer the plan and skip', () => {
  for (const kind of ['cycle', 'stuck']) {
    assert.deepEqual(ids(kind), ['openPlan', 'skip'], kind)
    assert.deepEqual(targets(kind), ['link', 'spec'], kind)
  }
})

test('pauseActions: owner, mainRed and config offer resume only', () => {
  for (const kind of ['owner', 'mainRed', 'config']) {
    assert.deepEqual(ids(kind), ['resume'], kind)
    assert.deepEqual(targets(kind), ['spec'], kind)
  }
})

test('pauseActions: a budget pause offers no button: the owner raises the budget line', () => {
  assert.deepEqual(ids('budget'), [])
})

test('pauseActions: a kind-less or unknown pause offers resume only', () => {
  assert.deepEqual(ids(undefined), ['resume'])
  assert.deepEqual(ids('somethingNew'), ['resume'])
  assert.deepEqual(ids('toString'), ['resume'])
})

test('pauseActions: only merge anyway asks and is owner-only', () => {
  for (const kind of allKinds) {
    for (const a of pauseActions(kind)) {
      assert.equal(!!a.confirm, a.id === 'merge', `${kind} ${a.id}`)
      assert.equal(!!a.ownerOnly, a.id === 'merge', `${kind} ${a.id}`)
    }
  }
})

test('pauseActions: callers cannot change the table', () => {
  pauseActions('owner')[0].label = 'changed'
  assert.equal(pauseActions('owner')[0].label, 'Resume')
})

const action = (id) => ({ id, label: id, target: 'spec' })

test('pauseCommand: the commands of each action', () => {
  const ctx = { issue: 7, head: 'abcdef0123456789' }
  assert.deepEqual(pauseCommand(action('retry'), ctx), { cmd: 'retry' })
  assert.deepEqual(pauseCommand(action('review'), ctx), { cmd: 'review' })
  assert.deepEqual(pauseCommand(action('resume'), ctx), { cmd: 'resume' })
  assert.deepEqual(pauseCommand(action('skip'), ctx), { cmd: 'skip', issue: 7 })
  assert.deepEqual(pauseCommand(action('merge'), ctx), {
    cmd: 'merge',
    issue: 7,
    sha: 'abcdef0123456789',
  })
  assert.deepEqual(pauseCommand(action('resume'), {}), { cmd: 'resume' })
})

test('pauseCommand: null for links and missing issue or head', () => {
  const ctx = { issue: 7, head: 'abc' }
  for (const id of ['openChat', 'openPipeline', 'openMR', 'openPlan']) {
    assert.equal(pauseCommand(action(id), ctx), null, id)
  }
  assert.equal(pauseCommand(action('skip'), {}), null)
  assert.equal(pauseCommand(action('skip'), { issue: 0 }), null)
  assert.equal(pauseCommand(action('merge'), { head: 'abc' }), null)
  assert.equal(pauseCommand(action('merge'), { issue: 7 }), null)
  assert.equal(pauseCommand(action('merge'), { issue: 7, head: '  ' }), null)
})

test('pauseEndsBy: every kind', () => {
  const gate = 'Continues by itself once a later run of #5 passes the gate.'
  for (const kind of ['needsInput', 'runFailed', 'findings', 'notReviewed', 'ciRed', 'ciCanceled']) {
    assert.equal(pauseEndsBy(kind, 5), gate, kind)
  }
  assert.equal(
    pauseEndsBy('reviewErrors', 5),
    `${gate} After a transient reviewer failure one re-review runs by itself.`,
  )
  assert.equal(pauseEndsBy('mergeRequest', 5), 'Continues by itself when the MR is merged by hand.')
  assert.equal(pauseEndsBy('mainRed'), 'Continues by itself when main is green.')
  assert.equal(pauseEndsBy('budget'), "Continues by itself once the milestone's budget line is raised above the spend.")
  assert.equal(
    pauseEndsBy('budget', 5),
    'Continues by itself once the budget of #5 is raised above its spend (`/agent budget <usd>` on the issue).',
  )
  for (const kind of ['owner', 'cycle', 'stuck', 'config', undefined, 'somethingNew']) {
    assert.equal(pauseEndsBy(kind, 5), '', String(kind))
  }
  assert.equal(pauseEndsBy('ciRed'), 'Continues by itself once a later run passes the gate.')
})

test('pauseTitle: every kind has its own heading', () => {
  const seen = new Set()
  for (const kind of allKinds) {
    const t = pauseTitle(kind)
    assert.ok(t && t !== 'Autopilot paused', kind)
    seen.add(t)
  }
  assert.equal(seen.size, allKinds.length)
  assert.equal(pauseTitle('reviewErrors'), 'Reviewers could not run')
  assert.equal(pauseTitle('ciRed'), 'CI is red')
  assert.equal(pauseTitle('owner'), 'Paused by an owner')
  assert.equal(pauseTitle(undefined), 'Autopilot paused')
  assert.equal(pauseTitle('somethingNew'), 'Autopilot paused')
})

test('reviewLimitLines: usage limits with and without a reset', () => {
  assert.deepEqual(
    reviewLimitLines([
      { reviewer: 'bugbot', kind: 'usageLimit', engine: 'cursor', resetAt: '2026-11-13T12:00:00Z' },
    ]),
    ['Cursor is limited until 13 Nov'],
  )
  assert.deepEqual(
    reviewLimitLines([{ reviewer: 'security', kind: 'usageLimit', engine: 'claude' }]),
    ['Claude is limited'],
  )
  assert.deepEqual(
    reviewLimitLines([{ reviewer: 'security', kind: 'usageLimit', resetAt: 'garbage' }]),
    ['Security is limited'],
  )
})

test('reviewLimitLines: transient and other failures', () => {
  assert.deepEqual(
    reviewLimitLines([
      { reviewer: 'bugbot', kind: 'transient' },
      { reviewer: 'security', kind: 'other' },
    ]),
    ['bugbot failed transiently', 'security failed'],
  )
})

test('reviewLimitLines: one line per engine or reviewer', () => {
  assert.deepEqual(
    reviewLimitLines([
      { reviewer: 'bugbot', kind: 'usageLimit', engine: 'cursor', resetAt: '2026-11-13T12:00:00Z' },
      { reviewer: 'security', kind: 'usageLimit', engine: 'cursor', resetAt: '2026-11-14T12:00:00Z' },
      { reviewer: 'bugbot', kind: 'transient' },
      { reviewer: 'bugbot', kind: 'other' },
      { reviewer: 'security', kind: 'usageLimit', engine: 'cursor' },
    ]),
    ['Cursor is limited until 14 Nov', 'bugbot failed'],
  )
  assert.deepEqual(reviewLimitLines(undefined), [])
  assert.deepEqual(reviewLimitLines([]), [])
})

test('reviewLimitLines: a custom date format', () => {
  assert.deepEqual(
    reviewLimitLines(
      [{ reviewer: 'bugbot', kind: 'usageLimit', engine: 'cursor', resetAt: '2026-11-13T12:00:00Z' }],
      (d) => d.toISOString().slice(0, 10),
    ),
    ['Cursor is limited until 2026-11-13'],
  )
})

test('commandAnswer: pending, answered, redacted and failed', () => {
  const cmd = { id: 9, at: 't', role: 'user', kind: 'command', body: '{"cmd":"resume"}', state: 'delivered' }
  assert.deepEqual(commandAnswer(undefined, 9), { state: 'pending' })
  assert.deepEqual(commandAnswer([cmd], 9), { state: 'pending' })
  const reply = { id: 10, at: 't', role: 'system', kind: 'message', body: ' Resumed. ', replyTo: 9 }
  assert.deepEqual(commandAnswer([reply, cmd], 9), { state: 'answered', text: 'Resumed.', redacted: false })
  assert.deepEqual(commandAnswer([{ ...reply, body: undefined, redacted: true }, cmd], 9), {
    state: 'answered',
    text: '',
    redacted: true,
  })
  assert.deepEqual(commandAnswer([{ ...reply, replyTo: 8 }, cmd], 9), { state: 'pending' })
  assert.deepEqual(commandAnswer([{ ...cmd, state: 'failed', reason: 'no pod' }], 9), {
    state: 'failed',
    reason: 'no pod',
  })
  assert.deepEqual(commandAnswer([{ ...cmd, state: 'failed' }], 9), {
    state: 'failed',
    reason: 'The command failed.',
  })
})

test('pausePrompt: the open prompt of mainRed and budget pauses', () => {
  const main = { id: 'Q1', kind: 'mainHealth', state: 'open' }
  const budget = { id: 'Q2', kind: 'budget', state: 'open' }
  const prompts = [main, budget]
  assert.equal(pausePrompt('mainRed', prompts), main)
  assert.equal(pausePrompt('budget', prompts), budget)
})

test('pausePrompt: no open prompt of the kind falls back to the card', () => {
  assert.equal(pausePrompt('mainRed', undefined), undefined)
  assert.equal(pausePrompt('mainRed', []), undefined)
  assert.equal(pausePrompt('mainRed', [{ id: 'Q1', kind: 'mainHealth', state: 'answered' }]), undefined)
  assert.equal(pausePrompt('mainRed', [{ id: 'Q2', kind: 'budget', state: 'open' }]), undefined)
  assert.deepEqual(ids('mainRed'), ['resume'])
})

test('pausePrompt: other kinds show no prompt and keep their actions', () => {
  const prompts = [
    { id: 'Q1', kind: 'mainHealth', state: 'open' },
    { id: 'Q2', kind: 'budget', state: 'open' },
  ]
  for (const kind of allKinds.filter((k) => k !== 'mainRed' && k !== 'budget')) {
    assert.equal(pausePrompt(kind, prompts), undefined, kind)
  }
  assert.equal(pausePrompt(undefined, prompts), undefined)
  assert.deepEqual(ids('owner'), ['resume'])
})

// Exit views as the milestone API sends them (aiv1.PauseExits with status
// and allowed, internal/ui/pause_exits.go), shortened to what wayOut reads.
const auto = (id, status = 'waiting', detail) => ({ id, auto: true, when: `${id} holds`, does: `${id} does`, status, detail })
const cmd = (id, command, target, allowed = true) => ({ id, command, target, does: `${id} does`, allowed })
const runExits = (allowed, ...more) => [
  auto('laterRunPasses'),
  auto('mergedByHand', 'unknown'),
  auto('closed', 'unknown'),
  cmd('retry', 'retry', 'implement', allowed),
  cmd('skip', 'skip', 'spec', allowed),
  ...more,
]
// exitsOf: every kind's exits for a viewer with chat (merge: also the merge grant).
function exitsOf(kind, { chat = true, merge = false } = {}) {
  const review = cmd('review', 'review', 'implement', chat)
  const mergeAnyway = cmd('mergeAnyway', 'merge', 'spec', chat && merge)
  const rebase = cmd('rebase', 'rebase', 'spec', false)
  const mergeBase = cmd('mergeBase', 'mergeBase', 'spec', false)
  const resume = cmd('resume', 'resume', 'spec', chat)
  const skip = cmd('skip', 'skip', 'spec', chat)
  return {
    owner: [resume],
    cycle: [{ id: 'changePlan', gitlab: true, when: 'change the milestone description', command: 'resume', does: 'replans' }, skip, resume],
    stuck: [
      auto('somethingCanStart'),
      { id: 'closeAsDone', label: 'done', target: 'implement', when: 'and close it', does: 'counts as merged' },
      { id: 'removeLink', gitlab: true, when: 'remove the "blocked by" link', does: 'no longer waits' },
      cmd('unskip', 'unskip', 'spec', false),
      cmd('startAnyway', 'startAnyway', 'spec', false),
      { id: 'pilotPrompt', prompt: true, when: 'with unskip, markDone or startAnyway', does: 'runs the tool' },
      skip,
      resume,
    ],
    needsInput: runExits(chat, { id: 'answered', label: 'ready', target: 'implement', when: 'after answering its questions', does: 'a new run starts' }),
    runFailed: runExits(chat),
    findings: runExits(chat, mergeAnyway),
    reviewErrors: runExits(chat, review, mergeAnyway),
    notReviewed: runExits(chat, review, mergeAnyway),
    ciRed: [auto('mainGreenRebase', 'met', 'main is green at ffe0123'), ...runExits(chat, rebase, mergeBase)],
    ciCanceled: runExits(chat),
    mergeRequest: [
      auto('mergedByHand', 'unknown'),
      auto('closed', 'unknown'),
      { id: 'fixInGitLab', gitlab: true, when: 'fix the merge request', command: 'resume', does: 'checks again' },
      rebase,
      mergeBase,
      skip,
    ],
    mainRed: [auto('mainGreen', 'waiting', 'main is red at abc1234'), auto('freezeOff'), auto('healthStale'), auto('mergedByHand', 'unknown'), auto('closed', 'unknown')],
    config: [auto('mergedByHand', 'unknown'), auto('closed', 'unknown'), { ...resume, when: 'after fixing the setup' }, skip],
    budget: [auto('budgetRaised', 'inProgress')],
  }[kind]
}

const buttons = (w) => w.remedies.filter((r) => r.action).map((r) => r.action.id)

test('wayOut: every kind shows all its exits, automatic ones with a status chip', () => {
  for (const kind of allKinds) {
    const exits = exitsOf(kind)
    const w = wayOut(exits)
    assert.ok(exits.length > 0, kind)
    assert.deepEqual([...w.auto, ...w.remedies].map((e) => e.id).sort(), exits.map((e) => e.id).sort(), kind)
    assert.deepEqual(w.auto.map((e) => e.id), exits.filter((e) => e.auto).map((e) => e.id), kind)
    for (const a of w.auto) assert.ok(a.chip && a.when && a.does, `${kind} ${a.id}`)
    for (const r of w.remedies) assert.ok(r.text || r.action, `${kind} ${r.id}`)
  }
})

test('wayOut: status chips and details', () => {
  const w = wayOut([
    auto('a', 'met', ' main is green at ffe0123 '),
    auto('b', 'inProgress'),
    auto('c', 'waiting'),
    auto('d', 'unknown'),
    auto('e', 'newer'),
    { id: 'f', auto: true, when: 'x', does: 'y' },
  ])
  assert.deepEqual(
    w.auto.map((a) => [a.status, a.chip, a.tone]),
    [
      ['met', 'lifts on the next poll', 'success'],
      ['inProgress', 'in progress', 'progress'],
      ['waiting', 'waiting', 'muted'],
      ['unknown', 'not known', 'muted'],
      ['unknown', 'not known', 'muted'],
      ['unknown', 'not known', 'muted'],
    ],
  )
  assert.equal(w.auto[0].detail, 'main is green at ffe0123')
  assert.equal(w.auto[0].when, 'A holds')
  assert.equal(w.remedies.length, 0)
})

test('wayOut: remedy commands map to the card actions and their targets', () => {
  const w = wayOut(exitsOf('reviewErrors', { merge: true }))
  const by = Object.fromEntries(w.remedies.filter((r) => r.action).map((r) => [r.id, r.action]))
  assert.deepEqual(
    Object.entries(by).map(([id, a]) => [id, a.id, a.target]),
    [
      ['retry', 'retry', 'implement'],
      ['skip', 'skip', 'spec'],
      ['review', 'review', 'implement'],
      ['mergeAnyway', 'merge', 'spec'],
    ],
  )
  assert.equal(by.mergeAnyway.confirm, true)
  assert.equal(by.mergeAnyway.ownerOnly, true)
  assert.equal(by.mergeAnyway.label, 'Merge anyway')
  assert.deepEqual(buttons(wayOut(exitsOf('owner'))), ['resume'])
  assert.equal(wayOut(exitsOf('owner')).remedies[0].action.target, 'spec')
  // The actions send the existing commands.
  const ctx = { issue: 7, head: 'abcdef0' }
  assert.deepEqual(
    w.remedies.filter((r) => r.action).map((r) => pauseCommand(r.action, ctx)),
    [{ cmd: 'retry' }, { cmd: 'skip', issue: 7 }, { cmd: 'review' }, { cmd: 'merge', issue: 7, sha: 'abcdef0' }],
  )
  // A remedy's condition is its text.
  const config = wayOut(exitsOf('config')).remedies.find((r) => r.id === 'resume')
  assert.equal(config.text, 'After fixing the setup')
  assert.equal(config.action.id, 'resume')
})

test('wayOut: remedies that are not allowed, or not Desk commands, give no button', () => {
  assert.deepEqual(buttons(wayOut(exitsOf('reviewErrors'))), ['retry', 'skip', 'review'])
  for (const kind of allKinds) {
    assert.deepEqual(buttons(wayOut(exitsOf(kind, { chat: false }))), [], kind)
  }
  const ci = wayOut(exitsOf('ciRed', { merge: true }))
  assert.deepEqual(buttons(ci), ['retry', 'skip'])
  const rebase = ci.remedies.find((r) => r.id === 'rebase')
  assert.equal(rebase.kind, 'command')
  assert.equal(rebase.command, 'rebase')
  assert.equal(rebase.text, 'on the tracking issue')
  // allowed from a confused server does not make rebase a button.
  assert.deepEqual(buttons(wayOut([{ ...cmd('rebase', 'rebase', 'spec'), allowed: true }])), [])
  assert.deepEqual(buttons(wayOut([cmd('x', 'constructor', 'spec')])), [])
  const noChat = wayOut(exitsOf('runFailed', { chat: false })).remedies.find((r) => r.id === 'retry')
  assert.deepEqual([noChat.kind, noChat.command, noChat.text], ['command', 'retry', 'on the issue'])
})

test('wayOut: labels, GitLab actions and prompts are text, with a link where the card has one', () => {
  const w = wayOut(exitsOf('stuck'))
  const by = Object.fromEntries(w.remedies.map((r) => [r.id, r]))
  assert.deepEqual([by.closeAsDone.kind, by.closeAsDone.text], ['label', 'Set the done label and close it'])
  assert.deepEqual([by.removeLink.kind, by.removeLink.text, by.removeLink.link], ['gitlab', 'In GitLab: remove the "blocked by" link', 'issue'])
  assert.deepEqual([by.pilotPrompt.kind, by.pilotPrompt.text], ['prompt', "Answer the pilot's prompt with unskip, markDone or startAnyway"])
  for (const id of ['closeAsDone', 'removeLink', 'pilotPrompt']) assert.equal(by[id].action, undefined, id)
  const plan = wayOut(exitsOf('cycle')).remedies[0]
  assert.deepEqual([plan.kind, plan.text, plan.link, plan.action], ['gitlab', 'In GitLab: change the milestone description, then resume', 'milestone', undefined])
  assert.equal(wayOut(exitsOf('mergeRequest')).remedies[0].link, 'mergeRequest')
  assert.equal(wayOut(exitsOf('needsInput')).remedies.find((r) => r.id === 'answered').link, 'issue')
})

test('wayOut: a command gives one button even when two exits name it', () => {
  const w = wayOut([cmd('resume', 'resume', 'spec'), { ...cmd('resume2', 'resume', 'spec'), when: 'after x' }])
  assert.deepEqual(buttons(w), ['resume'])
  assert.deepEqual([w.remedies[1].kind, w.remedies[1].text], ['command', 'on the tracking issue, after x'])
})

test('pauseActions with exits: the links stay, the commands come from wayOut, nothing twice', () => {
  for (const kind of allKinds) {
    const exits = exitsOf(kind, { merge: true })
    const row = pauseActions(kind, 5, exits)
    assert.ok(row.every((a) => a.target === 'link'), kind)
    assert.deepEqual(
      row.map((a) => a.id),
      pauseActions(kind, 5).filter((a) => a.target === 'link').map((a) => a.id),
      kind,
    )
    const all = [...row.map((a) => a.id), ...buttons(wayOut(exits))]
    assert.equal(new Set(all).size, all.length, `${kind}: ${all}`)
  }
  assert.deepEqual(
    pauseActions('findings', 5, exitsOf('findings')).map((a) => a.label),
    ['Open #5 chat'],
  )
})

test('pauseActions without exits: the kind buttons of an older operator', () => {
  for (const kind of allKinds) {
    assert.deepEqual(pauseActions(kind, 5, undefined), pauseActions(kind, 5), kind)
    assert.deepEqual(pauseActions(kind, 5, []), pauseActions(kind, 5), kind)
  }
  assert.deepEqual(ids('ciRed'), ['retry', 'openPipeline', 'skip'])
  assert.deepEqual(wayOut(undefined), { auto: [], remedies: [] })
  assert.equal(wayOutLine(undefined), '')
  assert.equal(wayOutLine([]), '')
})

test('wayOutLine: the first automatic exit with its status and the remedy count', () => {
  assert.equal(wayOutLine(exitsOf('ciRed')), 'Way out: MainGreenRebase holds — lifts on the next poll · 4 remedies')
  assert.equal(wayOutLine(exitsOf('budget')), 'Way out: BudgetRaised holds — in progress')
  assert.equal(wayOutLine(exitsOf('owner')), 'Way out: 1 remedy')
  assert.equal(wayOutLine(exitsOf('config', { chat: false })), 'Way out: MergedByHand holds — not known · 2 remedies')
})

test('deskCommand: the commands the card can send', () => {
  for (const c of ['resume', 'skip', 'merge', 'retry', 'review']) assert.equal(deskCommand(c), true, c)
  for (const c of ['rebase', 'mergeBase', 'unskip', 'startAnyway', '', undefined, 'toString']) assert.equal(deskCommand(c), false, String(c))
})
