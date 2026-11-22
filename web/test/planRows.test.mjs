// Tests of src/planRows.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  planDraftsCreated,
  planHeaderLines,
  planMilestoneLink,
  planMilestoneTitle,
  planPhaseTone,
  planSections,
  planSpendLabel,
  planTitle,
  raiseBudgetError,
} from '../src/planRows.ts'
import { plansPath } from '../src/plansPath.ts'

test('planMilestoneLink: the Desk page with a row, GitLab with Not planned, else null', () => {
  const base = { namespace: 'team', project: 'shop', createdMilestoneIID: 7, createdMilestoneURL: 'https://gitlab.example/m/7' }
  const milestone = { state: 'running', progress: { merged: 2, total: 4, open: 2 }, stored: false }
  assert.deepEqual(planMilestoneLink({ ...base, milestone }), {
    label: '%7',
    ref: '',
    to: { name: 'stream-milestone', params: { namespace: 'team', name: 'shop', iid: '7' } },
  })
  assert.deepEqual(planMilestoneLink(base), { label: '%7', ref: '', href: 'https://gitlab.example/m/7', note: 'Not planned' })
  assert.deepEqual(planMilestoneLink({ ...base, createdMilestoneURL: undefined }), { label: '%7', ref: '', href: '', note: 'Not planned' })
  assert.equal(planMilestoneLink({ namespace: 'team', project: 'shop' }), null)
  assert.equal(planMilestoneLink({ namespace: 'team', project: 'shop', createdMilestoneIID: 0, milestone }), null)
})

test('planMilestoneLink: the title where the page knows it, %N as secondary label', () => {
  const base = { namespace: 'team', project: 'shop', createdMilestoneIID: 7, createdMilestoneURL: 'https://gitlab.example/m/7' }
  const milestone = { state: 'running', progress: { merged: 2, total: 4, open: 2 }, stored: false }
  assert.deepEqual(planMilestoneLink({ ...base, milestone }, 'Guest checkout'), {
    label: 'Guest checkout',
    ref: '%7',
    to: { name: 'stream-milestone', params: { namespace: 'team', name: 'shop', iid: '7' } },
  })
  assert.deepEqual(planMilestoneLink(base, 'Guest checkout'), { label: 'Guest checkout', ref: '%7', href: 'https://gitlab.example/m/7', note: 'Not planned' })
  assert.equal(planMilestoneLink({ ...base, milestone }, '').label, '%7', 'a blank title is unknown')
})

test('planSections: Created apart, every other phase drafting, newest first', () => {
  const p = (name, phase, lastActivityAt) => ({ name, phase, lastActivityAt })
  const { drafting, created } = planSections([
    p('old', 'Idle', '2026-11-01T00:00:00Z'),
    p('failed', 'Failed', '2026-11-03T00:00:00Z'),
    p('done-old', 'Created', '2026-11-02T00:00:00Z'),
    p('working', 'Working', '2026-11-05T00:00:00Z'),
    p('done-new', 'Created', '2026-11-04T00:00:00Z'),
    p('new', '', '2026-11-06T00:00:00Z'),
  ])
  assert.deepEqual(drafting.map((x) => x.name), ['new', 'working', 'failed', 'old'])
  assert.deepEqual(created.map((x) => x.name), ['done-new', 'done-old'])
  assert.deepEqual(planSections([]), { drafting: [], created: [] })
})

test('plansPath: all plans, or one project encoded', () => {
  assert.equal(plansPath(), '/api/v1/plans')
  assert.equal(plansPath({ namespace: 'team', name: 'shop' }), '/api/v1/plans?project=team%2Fshop')
  assert.equal(plansPath({ namespace: 'a b', name: 'c&d' }), '/api/v1/plans?project=a%20b%2Fc%26d')
})

test('planTitle: trimmed title, else Untitled plan with the creation date', () => {
  assert.equal(planTitle('Refunds', '2026-11-01T00:00:00Z'), 'Refunds')
  assert.equal(planTitle('  Desk  ', '2026-11-01T00:00:00Z'), 'Desk')
  assert.equal(planTitle('', '2026-11-01T00:00:00Z'), `Untitled plan · ${new Date('2026-11-01T00:00:00Z').toLocaleDateString()}`)
  assert.equal(planTitle('   ', 'not-a-date'), 'Untitled plan')
})

test('planPhaseTone: each plan phase', () => {
  assert.equal(planPhaseTone('Starting'), 'info')
  assert.equal(planPhaseTone('Ready'), 'info')
  assert.equal(planPhaseTone('Working'), 'progress')
  assert.equal(planPhaseTone('Idle'), 'muted')
  assert.equal(planPhaseTone('Created'), 'success')
  assert.equal(planPhaseTone('Failed'), 'danger')
  assert.equal(planPhaseTone(undefined), 'muted')
  assert.equal(planPhaseTone(''), 'muted')
})

test('planSpendLabel: spend of cap', () => {
  assert.equal(planSpendLabel('1.2', '5'), '$1.20 of $5.00')
  assert.equal(planSpendLabel('0', '5.00'), '$0 of $5.00')
})

test('raiseBudgetError: above the cap, at most the limit', () => {
  assert.equal(raiseBudgetError('10', '5', '20'), '')
  assert.equal(raiseBudgetError('$20', '5', '20'), '')
  assert.equal(raiseBudgetError('', '5', '20'), 'Enter an amount in USD.')
  assert.equal(raiseBudgetError('ten', '5', '20'), 'Enter an amount in USD.')
  assert.equal(raiseBudgetError('5', '5', '20'), 'Enter more than $5.00 and at most $20.00.')
  assert.equal(raiseBudgetError('20.01', '5', '20'), 'Enter more than $5.00 and at most $20.00.')
})

test("planMilestoneTitle: the plan's title, else the draft's first heading", () => {
  assert.equal(planMilestoneTitle(' Refunds ', '# Other'), 'Refunds')
  assert.equal(planMilestoneTitle('', 'intro\n\n## Desk: plan milestones ##\n\n# Later'), 'Desk: plan milestones')
  assert.equal(planMilestoneTitle('', 'no heading\n#hashtag'), '')
  assert.equal([...planMilestoneTitle('', `# ${'é'.repeat(300)}`)].length, 255)
})

test('planHeaderLines: markers per toggle, then the owners lines', () => {
  assert.deepEqual(planHeaderLines(true, true).slice(0, 2), ['/agent spec', '/agent autopilot'])
  assert.deepEqual(planHeaderLines(false, true)[0], '/agent autopilot')
  assert.equal(planHeaderLines(false, false).length, 2)
  assert.match(planHeaderLines(true, false)[1], /^\/agent owners /)
})

test('planDraftsCreated: N of M for several drafts, the milestones after the first', () => {
  const m = (iid) => ({ iid, url: `https://gitlab.example/m/${iid}` })
  assert.equal(planDraftsCreated({ draftsTotal: 1, draftsCreated: 1, createdMilestones: [m(7)], createdMilestoneIID: 7 }), null)
  assert.equal(planDraftsCreated({ draftsTotal: 0, draftsCreated: 0, createdMilestones: [] }), null)
  assert.deepEqual(planDraftsCreated({ draftsTotal: 3, draftsCreated: 0, createdMilestones: [] }), { label: '0 of 3 created', others: [] })
  assert.deepEqual(planDraftsCreated({ draftsTotal: 3, draftsCreated: 2, createdMilestones: [m(7), m(9)], createdMilestoneIID: 7 }),
    { label: '2 of 3 created', others: [m(9)] })
})
