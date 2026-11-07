// Tests of src/crumbs.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { crumbsFor, issueLabel, milestoneLabel } from '../src/crumbs.ts'
import { isRouteName, routeNames } from '../src/routeNames.ts'

// Params every route can take: a route ignores the ones it has none of.
const params = { namespace: 'ns', name: 'p', iid: '3', issue: '12', task: 'issue-12', id: 'abcdef0123456789', rest: ['x', 'y'] }
const at = { namespace: 'ns', name: 'p' }

const overview = { label: 'Overview', to: { name: 'overview', params: {} } }
const project = { label: 'p', to: { name: 'stream-project', params: at } }
const milestone3 = (label) => ({ label, to: { name: 'stream-milestone', params: { ...at, iid: '3' } } })
const research = { label: 'Research', to: { name: 'researches', params: {} } }
const study = { label: 'p', to: { name: 'research', params: at } }
const runs = { label: 'Runs', to: { name: 'research-runs', params: at } }

test('every route has crumbs: links but the last, which names the view', () => {
  for (const name of routeNames) {
    const crumbs = crumbsFor({ name, params, query: { tab: 'spend', spendIssue: '12' } })
    assert.ok(crumbs.length > 0, `${name}: no crumbs`)
    for (const c of crumbs) assert.ok(c.label, `${name}: a crumb without a label`)
    for (const c of crumbs.slice(0, -1)) {
      assert.ok(c.to, `${name}: "${c.label}" is no link`)
      assert.ok(isRouteName(c.to.name), `${name}: "${c.label}" links to an unknown route`)
      assert.equal(c.to.query, undefined, `${name}: "${c.label}" keeps the query`)
      assert.ok(!('tab' in c.to.params), `${name}: "${c.label}" keeps the tab`)
    }
    assert.equal(crumbs.at(-1).to, undefined, `${name}: the last crumb is a link`)
  }
})

test('top-level views show one crumb', () => {
  assert.deepEqual(crumbsFor({ name: 'overview', params: {}, query: { tab: 'stream' } }), [{ label: 'Overview' }])
  assert.deepEqual(crumbsFor({ name: 'everything', params: {} }), [{ label: 'Overview' }])
  assert.deepEqual(crumbsFor({ name: 'inbox', params: {} }), [{ label: 'Inbox' }])
  assert.deepEqual(crumbsFor({ name: 'researches', params: {} }), [{ label: 'Research' }])
  assert.deepEqual(crumbsFor({ name: 'not-found', params: { rest: ['nope'] } }), [{ label: 'Not found' }])
})

test('project and milestone: the IID until the title is known', () => {
  assert.deepEqual(crumbsFor({ name: 'stream-project', params: at, query: { tab: 'board' } }), [overview, { label: 'p' }])
  assert.deepEqual(crumbsFor({ name: 'stream-milestone', params: { ...at, iid: '3' } }), [overview, project, { label: '%3' }])
  const names = { milestone: { ...at, iid: 3, title: 'Desk' } }
  assert.deepEqual(crumbsFor({ name: 'stream-milestone', params: { ...at, iid: '3' } }, names), [
    overview,
    project,
    { label: 'Desk' },
  ])
})

test('a name of another milestone or project is not used', () => {
  const route = { name: 'stream-milestone', params: { ...at, iid: '3' } }
  for (const m of [
    { ...at, iid: 4, title: 'Other' },
    { namespace: 'ns', name: 'q', iid: 3, title: 'Other' },
    { namespace: 'nt', name: 'p', iid: 3, title: 'Other' },
  ]) {
    assert.equal(crumbsFor(route, { milestone: m }).at(-1).label, '%3')
  }
})

test('the redirects show the page they lead to', () => {
  assert.deepEqual(crumbsFor({ name: 'project-board', params: at }), [overview, { label: 'p' }])
  assert.deepEqual(crumbsFor({ name: 'milestone', params: { ...at, iid: '3' } }), [overview, project, { label: '%3' }])
})

test('task: under the project until the names give its issue', () => {
  const route = { name: 'task', params: { ...at, task: 'issue-12' }, query: { tab: 'board' } }
  assert.deepEqual(crumbsFor(route), [overview, project, { label: 'issue-12' }])
  const plan = { task: { ...at, task: 'issue-12', milestone: 3 } }
  assert.deepEqual(crumbsFor(route, plan), [overview, project, { label: 'issue-12' }])
  const other = { task: { ...at, task: 'issue-13', milestone: 3, issue: 13 } }
  assert.deepEqual(crumbsFor(route, other), [overview, project, { label: 'issue-12' }])
})

test('issue: under its milestone, the IID until the titles are known', () => {
  const route = { name: 'issue', params: { ...at, iid: '3', issue: '12' }, query: { tab: 'stream' } }
  assert.deepEqual(crumbsFor(route), [overview, project, milestone3('%3'), { label: '#12' }])
  const names = { milestone: { ...at, iid: 3, title: 'Desk' }, issue: { ...at, iid: 12, title: 'Breadcrumbs' } }
  assert.deepEqual(crumbsFor(route, names), [overview, project, milestone3('Desk'), { label: 'Breadcrumbs' }])
  const other = { issue: { ...at, iid: 13, title: 'Other' } }
  assert.equal(crumbsFor(route, other).at(-1).label, '#12')
  // A malformed IID is no link to a milestone.
  const bad = { name: 'issue', params: { ...at, iid: 'x', issue: '12' } }
  assert.deepEqual(crumbsFor(bad), [overview, project, { label: '12' }])
})

test('task with an issue: under its issue page', () => {
  const route = { name: 'task', params: { ...at, task: 'issue-12' } }
  const task = { ...at, task: 'issue-12', milestone: 3, issue: 12 }
  const issue12 = (label) => ({ label, to: { name: 'issue', params: { ...at, iid: '3', issue: '12' } } })
  assert.deepEqual(crumbsFor(route, { task }), [
    overview,
    project,
    milestone3('%3'),
    issue12('#12'),
    { label: 'issue-12' },
  ])
  const names = {
    task,
    milestone: { ...at, iid: 3, title: 'Desk' },
    issue: { ...at, iid: 12, title: 'Breadcrumbs' },
  }
  assert.deepEqual(crumbsFor(route, names), [
    overview,
    project,
    milestone3('Desk'),
    issue12('Breadcrumbs'),
    { label: 'issue-12' },
  ])
})

test('research trail: research, runs, run', () => {
  assert.deepEqual(crumbsFor({ name: 'research-new', params: {} }), [research, { label: 'New research' }])
  assert.deepEqual(crumbsFor({ name: 'research', params: at }), [research, { label: 'p' }])
  assert.deepEqual(crumbsFor({ name: 'research-runs', params: at }), [research, study, { label: 'Runs' }])
  const route = { name: 'research-run', params: { ...at, id: 'abcdef0123456789' } }
  assert.deepEqual(crumbsFor(route), [research, study, runs, { label: 'abcdef01' }])
  const names = { run: { ...at, id: 'abcdef0123456789', title: 'Run of Nov 7, 07:00' } }
  assert.deepEqual(crumbsFor(route, names), [research, study, runs, { label: 'Run of Nov 7, 07:00' }])
})

test('labels: the title, else the IID', () => {
  assert.equal(milestoneLabel(3), '%3')
  assert.equal(milestoneLabel(3, ''), '%3')
  assert.equal(milestoneLabel(3, 'Desk'), 'Desk')
  assert.equal(issueLabel(12), '#12')
  assert.equal(issueLabel(12, 'Breadcrumbs'), 'Breadcrumbs')
})

test('isRouteName', () => {
  assert.ok(isRouteName('task'))
  assert.ok(!isRouteName('nope'))
  assert.ok(!isRouteName(undefined))
})
