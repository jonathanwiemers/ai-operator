// Tests of src/railItems.ts: npm test (node --test, no extra dependencies).
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { openSets, railItems } from '../src/railItems.ts'

const project = (name, over = {}) => ({ namespace: 'agents', name, stages: {}, ...over })
const set = (name, over = {}) => ({ namespace: 'agents', name, counts: { active: 2 }, ...over })
const all = { history: true, plans: true, research: true }
const input = (over = {}) => ({ path: '/', inboxCount: 0, projects: [], projectSets: [], researches: [], features: all, ...over })
const labels = (items) => items.map((i) => i.label)
const find = (groups, label) => groups.flat().flatMap((i) => [i, ...(i.children ?? [])]).find((i) => i.label === label)

test('railItems: the pages, Projects and Research groups', () => {
  const g = railItems(input())
  assert.equal(g.length, 3)
  assert.deepEqual(labels(g[0]), ['Inbox', 'Overview', 'Metrics', 'Plans'])
  assert.deepEqual(
    g[0].map((i) => i.to),
    ['/inbox', '/', '/metrics', '/plans'],
  )
  for (const i of g[0]) assert.match(i.icon, /^i-lucide-/)
  assert.deepEqual(g[1][0], { label: 'Projects', type: 'label' })
  assert.deepEqual(g[2][0], { label: 'Research', type: 'label' })
})

test('railItems: features leave out Metrics, Plans and the Research group', () => {
  const g = railItems(input({ features: { history: false, plans: false, research: false } }))
  assert.equal(g.length, 2)
  assert.deepEqual(labels(g[0]), ['Inbox', 'Overview'])
})

test('railItems: the inbox badge carries the count in the danger tone, none at 0', () => {
  assert.equal(find(railItems(input()), 'Inbox').badge, undefined)
  assert.deepEqual(find(railItems(input({ inboxCount: 3 })), 'Inbox').badge, { label: '3', color: 'error', variant: 'soft' })
})

test('railItems: collapsed, the pages only, the inbox count on the icon', () => {
  const g = railItems(input({ collapsed: true, inboxCount: 4, projects: [project('shop')] }))
  assert.equal(g.length, 1)
  assert.deepEqual(labels(g[0]), ['Inbox', 'Overview', 'Metrics', 'Plans'])
  assert.deepEqual(g[0][0].chip, { text: 4, color: 'error', size: '3xl' })
  assert.equal(railItems(input({ collapsed: true })).at(0)[0].chip, undefined)
  assert.equal(railItems(input({ inboxCount: 4 })).at(0)[0].chip, undefined, 'expanded: the badge, no chip')
})

test('railItems: collapsed, the hidden labels become the accessible names', () => {
  const names = (g) => g[0].map((i) => i['aria-label'])
  assert.deepEqual(names(railItems(input({ collapsed: true, inboxCount: 4 }))), ['Inbox (4)', 'Overview', 'Metrics', 'Plans'])
  assert.deepEqual(names(railItems(input({ collapsed: true }))), ['Inbox', 'Overview', 'Metrics', 'Plans'])
  assert.deepEqual(names(railItems(input({ inboxCount: 4 }))), [undefined, undefined, undefined, undefined], 'expanded: the label is the name')
})

test('railItems: one item per project with its state, count and active state', () => {
  const g = railItems(
    input({
      path: '/projects/agents/shop/milestones/3',
      projects: [
        project('shop', { stages: { working: 1, 'needs-input': 2 }, activeMilestones: 1, settlingMilestones: 1 }),
        project('docs', { stages: { failed: 1 } }),
        project('infra'),
      ],
    }),
  )
  const [shop, docs, infra] = g[1].slice(1)
  assert.deepEqual(labels([shop, docs, infra]), ['shop', 'docs', 'infra'])
  assert.equal(shop.to, '/projects/agents/shop')
  assert.equal(shop.icon, 'i-lucide-folder-git-2')
  assert.equal(shop.active, true, 'lit on a page below it')
  assert.equal(docs.active, false)
  assert.deepEqual(shop.chip, { color: 'info' })
  assert.deepEqual(docs.chip, { color: 'warning' })
  assert.deepEqual(infra.chip, { color: 'success' })
  assert.deepEqual(shop.badge, { label: '2', color: 'error', variant: 'soft' })
  assert.equal(infra.badge, undefined)
  assert.match(shop.description, /1 active · 1 settling$/)
  assert.match(shop.hint, /^settling/)
  assert.equal(infra.hint, undefined)
})

test('railItems: a project path is not a prefix match on its name', () => {
  const g = railItems(input({ path: '/projects/agents/shop-two', projects: [project('shop'), project('shop-two')] }))
  assert.deepEqual(
    g[1].slice(1).map((i) => i.active),
    [false, true],
  )
})

test('railItems: a set is a trigger with its children, open when the route is inside', () => {
  const projects = [project('ai-operator'), project('b', { set: 'jo' }), project('a', { set: 'jo' })]
  const sets = [set('jo', { lastDiscoveryTime: '2026-11-20T08:00:00Z' })]
  const outside = railItems(input({ path: '/inbox', projects, projectSets: sets }))
  assert.deepEqual(labels(outside[1]), ['Projects', 'ai-operator', 'jo'])
  const jo = outside[1][2]
  assert.equal(jo.type, 'trigger')
  assert.equal(jo.slot, 'set')
  assert.equal(jo.to, undefined, 'a set has no page')
  assert.equal(jo.value, 'set:agents/jo')
  assert.equal(jo.defaultOpen, false)
  assert.equal(jo.description, '2 active')
  assert.equal(jo.discoveredAt, '2026-11-20T08:00:00Z')
  assert.equal(jo.problem, null)
  assert.deepEqual(labels(jo.children), ['a', 'b'])
  assert.deepEqual(
    jo.children.map((c) => c.to),
    ['/projects/agents/a', '/projects/agents/b'],
  )
  assert.deepEqual(openSets(outside), [])

  const inside = railItems(input({ path: '/projects/agents/b/tasks/t1', projects, projectSets: sets }))
  const open = inside[1][2]
  assert.equal(open.defaultOpen, true)
  assert.deepEqual(
    open.children.map((c) => c.active),
    [false, true],
  )
  assert.deepEqual(openSets(inside), ['set:agents/jo'])
})

test('railItems: a set with a False condition carries the problem', () => {
  const sets = [set('jo', { conditions: [{ type: 'Ready', status: 'False', reason: 'NoToken' }] })]
  assert.equal(find(railItems(input({ projectSets: sets })), 'jo').problem, 'NoToken')
})

test('railItems: no projects says so; not loaded says nothing', () => {
  assert.deepEqual(labels(railItems(input())[1]), ['Projects', 'No projects'])
  assert.deepEqual(labels(railItems(input({ projects: null, projectSets: null }))[1]), ['Projects'])
})

test('railItems: research items with their sub-line, then All research', () => {
  const g = railItems(
    input({
      path: '/research/agents/news/runs/7',
      researches: [
        { namespace: 'agents', name: 'news', schedule: '15 7 * * *', timeZone: 'Europe/Berlin', phase: 'Succeeded', oneOff: false },
        { namespace: 'agents', name: 'scan', phase: 'Running', schedule: '0 * * * *', oneOff: false },
        { namespace: 'agents', name: 'once', oneOff: true },
      ],
    }),
  )
  const r = g[2]
  assert.deepEqual(labels(r), ['Research', 'news', 'scan', 'once', 'All research'])
  assert.equal(r[1].to, '/research/agents/news')
  assert.equal(r[1].icon, 'i-lucide-flask-conical')
  assert.equal(r[1].active, true, 'lit on its runs')
  assert.deepEqual(
    r.slice(1, 4).map((i) => i.description),
    ['daily at 07:15 Europe/Berlin', 'running', 'one-off'],
  )
  assert.equal(r[1].hint, '15 7 * * * Europe/Berlin', 'the raw cron in the tooltip')
  const raw = railItems(input({ researches: [{ namespace: 'a', name: 'r', schedule: '0 0 1 * *', oneOff: false }] }))[2][1]
  assert.equal(raw.description, '0 0 1 * *', 'a form without words shows raw')
  assert.deepEqual(
    r.slice(1, 4).map((i) => i.chip),
    [{ color: 'success' }, { color: 'info' }, undefined],
  )
  assert.equal(r[4].to, '/research')
  assert.equal(r[4].active, false)
  assert.equal(railItems(input({ path: '/research' }))[2].at(-1).active, true)
})

test('railItems: the active page from the path', () => {
  const active = (path) => railItems(input({ path }))[0].filter((i) => i.active).map((i) => i.label)
  assert.deepEqual(active('/'), ['Overview'])
  assert.deepEqual(active('/inbox'), ['Inbox'])
  assert.deepEqual(active('/metrics'), ['Metrics'])
  assert.deepEqual(active('/plans'), ['Plans'])
  assert.deepEqual(active('/projects/agents/shop/plans'), [])
})

test('railItems: every link calls onSelect, labels and triggers do not', () => {
  const onSelect = () => {}
  const g = railItems(input({ onSelect, projects: [project('b', { set: 'jo' })], projectSets: [set('jo')], researches: [{ namespace: 'a', name: 'r', oneOff: true }] }))
  for (const i of g.flat().flatMap((i) => [i, ...(i.children ?? [])])) {
    assert.equal(i.onSelect, i.to ? onSelect : undefined, i.label)
  }
})
