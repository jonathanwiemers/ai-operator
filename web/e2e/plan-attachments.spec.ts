// Files attached to a plan chat message (R6 of the milestone "Desk plans:
// attach screenshots and files"): a pasted screenshot, a .log from the
// paperclip and a dropped PDF show as chips; a removed chip uploads
// nothing; Send uploads the others in order, then posts the text with one
// line per file. The browser refuses a .zip, a
// file over 10 MiB and a sixth file by name and rule. Without a connected
// GitLab the upload answers 409: nothing is posted and the composer offers
// to connect. Run with npm run e2e (built UI, Chromium).
import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { answer, now } from './fixtures'
import { features } from './fixtures/session'
import { journeyAt } from './journey'

const planPath = '/projects/team-a/tools-foo/plans/plan-wait'
const uploadPath = '/projects/team-a/tools-foo/plans/uploads'

async function open(page: Page, baseURL: string | undefined, opts: { uploadStatus?: number } = {}) {
  const problems: string[] = []
  const uploads: string[] = []
  const posts: { body: string }[] = []
  const origin = new URL(baseURL ?? '').origin
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.clock.setFixedTime(now)
  page.on('pageerror', (err) => problems.push(err.message))
  await page.route((url) => url.protocol !== 'data:' && url.protocol !== 'blob:' && url.origin !== origin, (r) => r.abort('blockedbyclient'))
  await page.route('**/api/v1/**', async (r) => {
    const req = r.request()
    const url = new URL(req.url())
    const p = url.pathname.slice('/api/v1'.length)
    if (p === '/events') return r.fulfill({ status: 200, contentType: 'text/event-stream', body: 'retry: 3600000\n\n' })
    if (req.method() === 'POST' && p === uploadPath) {
      const name = /filename="([^"]+)"/.exec(req.postDataBuffer()?.toString('latin1') ?? '')?.[1] ?? '?'
      uploads.push(name)
      if (opts.uploadStatus) return r.fulfill({ status: opts.uploadStatus, json: { error: 'connect your GitLab account first' } })
      const secret = String(uploads.length).repeat(32)
      const md = `${name.endsWith('.png') ? '!' : ''}[${name}](/uploads/${secret}/${name})`
      return r.fulfill({ status: 201, json: { name, size: 10, contentType: 'image/png', url: `/uploads/${secret}/${name}`, markdown: md } })
    }
    if (req.method() === 'POST' && p === `${planPath}/chat`) {
      posts.push(req.postDataJSON())
      return r.fulfill({ status: 200, json: { id: 1 } })
    }
    if (p === '/features') return r.fulfill({ status: 200, json: { features: [...features.features, { name: 'plans' }] } })
    // The % menu's milestones, loaded when the text first has the focus.
    if (p === '/milestones') return r.fulfill({ status: 200, json: { milestones: [] } })
    if (p === '/gitlab') return r.fulfill({ status: 200, json: { connected: false, login: '/oauth2/gitlab/login' } })
    const body = answer(p, url.searchParams)
    if (req.method() !== 'GET' || body === undefined) {
      problems.push(`${req.method()} ${url.pathname}`)
      return r.fulfill({ status: 404, json: { error: 'no fixture' } })
    }
    return r.fulfill({ status: 200, json: body })
  })
  await page.goto(planPath)
  await expect(composer(page).getByRole('combobox', { name: 'Message' })).toBeVisible()
  return { problems, uploads, posts }
}

// The plan chat's text is the % menu's combobox (Composer's references),
// named Message.
const composer = (page: Page) => page.locator('main [data-plan-chat] .composer')
const chips = (page: Page) => composer(page).locator('[data-attachment-chip]')

// paste and drop dispatch the events a browser sends to the text, with
// files of size bytes each (the content is not checked here, the server
// sniffs it); a drop bubbles to the composer's box.
type Spec = { name: string; type: string; size?: number }
async function dispatch(page: Page, kind: 'paste' | 'drop', files: Spec[]) {
  await page.locator('main [data-plan-chat] textarea').evaluate(
    (el, { kind, files }) => {
      const dt = new DataTransfer()
      for (const f of files) dt.items.add(new File([new Uint8Array(f.size ?? 10)], f.name, { type: f.type }))
      if (kind === 'paste') el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
      else {
        el.dispatchEvent(new DragEvent('dragover', { dataTransfer: dt, bubbles: true, cancelable: true }))
        el.dispatchEvent(new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true }))
      }
    },
    { kind, files },
  )
}

async function attachWithPaperclip(page: Page, files: { name: string; mimeType: string; buffer: Buffer }[]) {
  const chooser = page.waitForEvent('filechooser')
  await composer(page).getByRole('button', { name: 'Attach files' }).click()
  await (await chooser).setFiles(files)
}

test('paste, paperclip and drop show chips; Send uploads the kept files, then posts their lines@1280', journeyAt(1280), async ({ page, baseURL }) => {
  const { problems, uploads, posts } = await open(page, baseURL)
  await composer(page).getByRole('combobox', { name: 'Message' }).fill('The header overlaps the tabs')
  await dispatch(page, 'paste', [{ name: 'image.png', type: 'image/png' }])
  await attachWithPaperclip(page, [{ name: 'build.log', mimeType: 'text/plain', buffer: Buffer.from('log line\n') }])
  await dispatch(page, 'drop', [{ name: 'spec.pdf', type: 'application/pdf' }])
  await expect(chips(page)).toHaveCount(3)
  await expect(chips(page)).toContainText(['pasted-1.png', 'build.log', 'spec.pdf'])

  const { violations } = await new AxeBuilder({ page }).include('main [data-plan-chat]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']).analyze()
  expect(violations.map((v) => v.id)).toEqual([])

  await composer(page).getByRole('button', { name: 'Remove spec.pdf' }).click()
  await expect(chips(page)).toHaveCount(2)
  await composer(page).getByRole('button', { name: 'Send' }).click()
  await expect(chips(page)).toHaveCount(0)
  expect(uploads).toEqual(['pasted-1.png', 'build.log'])
  expect(posts.map((p) => p.body)).toEqual([
    'The header overlaps the tabs\n' +
      `![pasted-1.png](/uploads/${'1'.repeat(32)}/pasted-1.png)\n` +
      `[build.log](/uploads/${'2'.repeat(32)}/build.log)`,
  ])
  await expect(composer(page).getByRole('combobox', { name: 'Message' })).toHaveValue('')
  expect(problems).toEqual([])
})

test('a .zip, a 12 MiB PNG and a sixth file are refused by name and rule@1280', async ({ page, baseURL }) => {
  const { problems, uploads } = await open(page, baseURL)
  await dispatch(page, 'drop', [
    { name: 'f1.txt', type: 'text/plain' },
    { name: 'f2.txt', type: 'text/plain' },
    { name: 'f3.txt', type: 'text/plain' },
    { name: 'f4.txt', type: 'text/plain' },
    { name: 'f5.txt', type: 'text/plain' },
    { name: 'x.zip', type: 'application/zip' },
    { name: 'big.png', type: 'image/png', size: 12 * 1024 * 1024 },
    { name: 'f6.txt', type: 'text/plain' },
  ])
  await expect(chips(page)).toHaveCount(5)
  await expect(composer(page).locator('[data-attachment-refused]')).toHaveText([
    'x.zip: only PNG, JPG, GIF, WebP, PDF and text files',
    'big.png: larger than 10 MiB',
    'f6.txt: at most 5 files per message',
  ])
  expect(uploads).toEqual([])
  expect(problems).toEqual([])
})

test('without a connected GitLab, Send with a file posts nothing and offers to connect@1280', async ({ page, baseURL }) => {
  const { problems, uploads, posts } = await open(page, baseURL, { uploadStatus: 409 })
  await composer(page).getByRole('combobox', { name: 'Message' }).fill('See the screenshot')
  await dispatch(page, 'paste', [{ name: '', type: 'image/png' }])
  await composer(page).getByRole('button', { name: 'Send' }).click()
  await expect(composer(page)).toContainText('Connect your GitLab account to attach files.')
  await expect(composer(page).getByRole('link', { name: 'Connect GitLab' })).toHaveAttribute('href', /^\/oauth2\/gitlab\/login\?return=/)
  await expect(chips(page)).toHaveCount(1)
  await expect(composer(page).getByRole('combobox', { name: 'Message' })).toHaveValue('See the screenshot')
  expect(uploads).toEqual(['pasted-1.png'])
  expect(posts).toEqual([])
  expect(problems).toEqual([])
})
