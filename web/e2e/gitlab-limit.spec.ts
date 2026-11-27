// GitLab's rate limit on the Desk (R11, R12 of docs/specs/gitlab-rate-limit.spec.md):
// the task banner of a run deferred on GitLab, the "GitLab limit" badge in
// the milestone's issue rows and queue, and the bot's usage and the
// project's limit in the project header. Each page is checked with axe and
// shot at 1440 px light and dark and at 390 px dark for the merge request.
// Run with npm run e2e (built UI, Chromium).
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { answer, now } from './fixtures'
import { milestones, shopDetail } from './fixtures/stream'
import { refundsResponse } from './fixtures/task'
import type { GitLabRateLimitStatus, MilestoneView, ProjectDetail, TaskResponse } from '../src/types'

const shopPath = '/projects/team-a/shop'
const taskPath = `${shopPath}/tasks/${refundsResponse.task.name}`
const at = (min: number) => new Date(now.getTime() + min * 60_000).toISOString()
const deferral = { blockReason: 'gitlab' as const, deferredUntil: at(12), deferrals: 1 }

const deferredTask: TaskResponse = {
  ...refundsResponse,
  task: {
    ...refundsResponse.task,
    runPending: true,
    status: { ...refundsResponse.task.status, run: { ...refundsResponse.task.status.run, ...deferral, lastResult: 'Deferred' } },
  },
}
const m14 = milestones['14']!
const deferredMilestone: MilestoneView = {
  ...m14,
  issues: m14.issues?.map((i) => (i.iid === 281 ? { ...i, ...deferral } : i)),
  autopilot: m14.autopilot && { ...m14.autopilot, items: m14.autopilot.items?.map((q) => (q.iid === 281 ? { ...q, ...deferral } : q)) },
}
const rateLimit = (r: GitLabRateLimitStatus): ProjectDetail => ({ ...shopDetail, gitLab: { rateLimit: r } })

// open serves the fixtures with the deferred task and milestone and the
// project p, and opens path.
async function open(page: Page, baseURL: string | undefined, path: string, p: ProjectDetail, size = { width: 1440, height: 900 }, scheme: 'light' | 'dark' = 'light') {
  const origin = new URL(baseURL ?? '').origin
  const problems: string[] = []
  await page.setViewportSize(size)
  await page.emulateMedia({ colorScheme: scheme })
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const api = url.pathname.slice('/api/v1'.length)
    if (api === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (req.method() === 'POST' && api.endsWith('/warm')) return r.fulfill({ status: 204 })
    const body =
      api === taskPath ? deferredTask : api === `${shopPath}/milestones/14` ? deferredMilestone : api === shopPath ? p : answer(api, url.searchParams)
    if (req.method() !== 'GET' || body === undefined) {
      problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    if (typeof body === 'string') return r.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body })
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(path)
  return problems
}

async function axe(page: Page, part: string) {
  const { violations } = await new AxeBuilder({ page }).include(part).withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
  expect(violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([])
}

// shots: the merge request's screenshots of one part of the page, at
// 1440 px light and dark and 390 px dark.
async function shots(page: Page, testInfo: TestInfo, name: string, path: string, part: string) {
  for (const [size, scheme] of [
    [{ width: 1440, height: 900 }, 'light'],
    [{ width: 1440, height: 900 }, 'dark'],
    [{ width: 390, height: 844 }, 'dark'],
  ] as const) {
    await page.setViewportSize(size)
    await page.emulateMedia({ colorScheme: scheme })
    await page.goto(path)
    const el = page.locator(part).first()
    await expect(el).toBeVisible()
    await axe(page, part)
    await el.screenshot({ path: join(testInfo.project.outputDir, '..', 'screenshots', `${name}@${size.width}-${scheme}.png`) })
  }
}

test('task: a GitLab deferral has its own banner@1440', async ({ page, baseURL }, testInfo) => {
  const problems = await open(page, baseURL, taskPath, shopDetail)
  const banner = page.locator('main [data-gitlab-deferral]')
  await expect(banner).toContainText('Rate limited by GitLab')
  await expect(banner).toContainText(/The run starts again in 12 min\w* \(at \d{1,2}:\d{2}( [AP]M)?, deferred 1×\)\./)
  await expect(page.locator('main').getByText('Waiting for a usage limit')).toHaveCount(0)
  await shots(page, testInfo, 'gitlab-task-banner', taskPath, 'main [data-gitlab-deferral]')
  expect(problems).toEqual([])
})

test('milestone: the deferred issue has the GitLab limit badge in its row and the queue@1440', async ({ page, baseURL }, testInfo) => {
  const path = `${shopPath}/milestones/14?tab=issues&view=lanes`
  const problems = await open(page, baseURL, path, shopDetail)
  const row = page.locator('main [data-issue]').filter({ hasText: '#281' })
  await expect(row.locator('[data-gitlab-limit]')).toHaveText(/^GitLab limit · \d{1,2}:\d{2}( [AP]M)?$/)
  await expect(page.locator('main [data-queue] [data-queue-item]').filter({ hasText: '#281' }).locator('[data-gitlab-limit]')).toBeVisible()
  await shots(page, testInfo, 'gitlab-milestone-row', path, 'main [data-issue]:has([data-gitlab-limit])')
  expect(problems).toEqual([])
})

test('project header: GitLab usage, low at the reserve, and the limit@1440', async ({ page, baseURL }, testInfo) => {
  const usage = rateLimit({ observed: 63, remaining: 37, limit: 100, resetAt: at(0.5) })
  const problems = await open(page, baseURL, shopPath, usage)
  const head = page.locator('main [data-stream-head]')
  const bar = head.locator('[data-gitlab-usage-bar]')
  await expect(bar).toHaveText('GitLab API 63/100 this minute')
  await expect(bar).not.toHaveAttribute('data-low')
  await expect(bar.getByRole('progressbar')).toHaveAttribute('aria-label', 'GitLab API 63/100 this minute')
  await expect(head.locator('[data-gitlab-limited]')).toHaveCount(0)
  await axe(page, 'main [data-gitlab-usage]')

  const limited = rateLimit({ observed: 100, limit: 100, resetAt: at(0.5), limitedUntil: at(6) })
  await page.unrouteAll({ behavior: 'ignoreErrors' })
  problems.push(...(await open(page, baseURL, shopPath, limited)))
  await expect(bar).toHaveText('GitLab API 100/100 this minute')
  await expect(bar).toHaveAttribute('data-low', 'true')
  await expect(head.locator('[data-gitlab-limited]')).toHaveText(/^GitLab rate limit until \d{1,2}:\d{2}( [AP]M)?$/)
  await shots(page, testInfo, 'gitlab-project-header', shopPath, 'main [data-stream-head]')
  expect(problems).toEqual([])
})

test('project header: no GitLab line without a fresh reading@1440', async ({ page, baseURL }) => {
  const problems = await open(page, baseURL, shopPath, rateLimit({ observed: 90, limit: 100, resetAt: at(-2) }))
  await expect(page.locator('main [data-stream-head] h1')).toBeVisible()
  await expect(page.locator('main [data-pinned-milestone="14"]').first()).toBeVisible()
  await expect(page.locator('main [data-gitlab-usage]')).toHaveCount(0)
  expect(problems).toEqual([])
})
