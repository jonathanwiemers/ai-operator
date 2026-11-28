// The service worker (R6 of the Desk notifications milestone, #798): it
// registers at scope /, and a window it opens on /?open=<item> lands on the
// item. Run with npm run e2e (built UI, Chromium). Not in the journey set,
// which is full: a notification click has no daily path of its own here.
import { expect, test, type Page } from '@playwright/test'
import { answer, answerProblems, now } from './fixtures'
import { paymentsMilestone } from './fixtures/stream'
import type { InboxItem, MilestoneView, PromptView } from '../src/types'

// A milestone with issue 281 stuck and its assessment prompt, as in
// needs-you.spec.ts.
const q3 = paymentsMilestone.prompts!.find((p) => p.id === 'Q3')!
const q8: PromptView = {
  ...q3, id: 'Q8', issueIID: 281, kind: 'assessment', question: 'Q8: rerun #281?', state: 'open',
  answer: undefined, closedAt: undefined, closedReason: undefined, closedBy: undefined, canAnswer: true,
}
const stuck: MilestoneView = {
  ...paymentsMilestone,
  autopilot: {
    ...paymentsMilestone.autopilot!, pauseKind: 'stuck', pausedFor: undefined, pauseIssue: 281,
    holds: [{ ...paymentsMilestone.autopilot!.holds![0]!, iid: 281, title: 'Support refunds', kind: 'needsInput' }],
  },
  pilot: { ...paymentsMilestone.pilot!, proposals: [], acted: [], interventions: [] }, prompts: [q8], pauses: undefined,
}
const milestoneAPI = '/projects/team-a/shop/milestones/14'

// The problem's Inbox item, as a notification's data carries it.
const problem: InboxItem = {
  namespace: 'team-a', project: 'shop', kind: 'problem', task: 'shop-spec-m14',
  milestoneIID: 14, issueIID: 281, title: 'Support refunds', reason: 'the run failed',
  key: 'team-a/shop/problem/14/281/stuck', notify: 'problem',
}

async function open(page: Page, baseURL: string | undefined, path: string) {
  const origin = new URL(baseURL ?? '').origin
  const errors: string[] = []
  await page.clock.install({ time: now })
  page.on('pageerror', (err) => errors.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const url = new URL(r.request().url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    const pref = `${milestoneAPI}/problems`
    let body: unknown
    if (p === milestoneAPI) body = stuck
    else if (p === pref) body = answerProblems(stuck, undefined)
    else if (p.startsWith(`${pref}/`)) body = answerProblems(stuck, p.slice(pref.length + 1))
    else body = answer(p, url.searchParams)
    if (body === undefined) return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  return errors
}

test('the service worker registers at scope /@1280', async ({ page, baseURL }) => {
  const errors = await open(page, baseURL, '/')
  const scope = await page.evaluate(async () => (await navigator.serviceWorker.ready).scope)
  expect(new URL(scope).pathname).toBe('/')
  const script = await page.evaluate(async () => (await navigator.serviceWorker.ready).active?.scriptURL)
  expect(new URL(script!).pathname).toBe('/sw.js')
  expect(errors).toEqual([])
})

test('/?open= with a problem lands on its card and drops the query@1280', async ({ page, baseURL }) => {
  const errors = await open(page, baseURL, `/?open=${encodeURIComponent(JSON.stringify({ item: problem }))}`)
  await expect(page).toHaveURL(/\/projects\/team-a\/shop\/milestones\/14\/problems\/issue-281$/)
  expect(new URL(page.url()).searchParams.has('open')).toBe(false)
  await expect(page.locator('[data-problem-thread="issue-281"], [data-problem-thread="1281"], [data-problem-thread="2281"]')).toBeVisible()
  expect(errors).toEqual([])
})

test('/?open= with garbage only drops the query@1280', async ({ page, baseURL }) => {
  const errors = await open(page, baseURL, '/?open=%7Bnope')
  await expect.poll(() => new URL(page.url()).searchParams.has('open')).toBe(false)
  expect(new URL(page.url()).pathname).toBe('/')
  expect(errors).toEqual([])
})

test('a click message with a reply port routes to the problem and answers opened@1280', async ({ page, baseURL }) => {
  const errors = await open(page, baseURL, '/')
  // The listener lives in the lazy deskWorker chunk: send until it answers.
  const reply = await page.evaluate(async (item) => {
    for (let i = 0; i < 50; i++) {
      const ch = new MessageChannel()
      const answered = new Promise<unknown>((resolve) => {
        ch.port1.onmessage = (m) => resolve(m.data)
        setTimeout(() => resolve(null), 200)
      })
      navigator.serviceWorker.dispatchEvent(new MessageEvent('message', { data: { open: { item } }, ports: [ch.port2] }))
      const data = await answered
      if (data) return data
    }
    return null
  }, problem)
  expect(reply).toEqual({ opened: true })
  await expect(page).toHaveURL(/\/projects\/team-a\/shop\/milestones\/14\/problems\/issue-281$/)
  await expect(page.locator('[data-problem-thread="issue-281"], [data-problem-thread="1281"], [data-problem-thread="2281"]')).toBeVisible()
  expect(errors).toEqual([])
})
