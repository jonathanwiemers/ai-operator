// The Desk is installable (R9, #801): the manifest, the icons it names and
// the index.html links. Run with npm run e2e (built UI, Chromium).
import { expect, test } from '@playwright/test'
import { journeyAt } from './journey'

interface Icon {
  src: string
  sizes: string
  type: string
  purpose?: string
}

// The width and height of a PNG are big-endian uint32s at bytes 16 and 20.
const pngSize = (b: Buffer) => `${b.readUInt32BE(16)}x${b.readUInt32BE(20)}`

test('the manifest is installable and its icons exist@1280', journeyAt(1280), async ({ request }) => {
  const res = await request.get('/manifest.webmanifest')
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toContain('application/manifest+json')
  const m = await res.json()
  expect(m.display).toBe('standalone')
  expect(m.start_url).toBe('/')
  expect(m.scope).toBe('/')
  expect(m.name).toBe('ai-operator Desk')
  expect(m.short_name).toBe('Desk')
  const icons: Icon[] = m.icons
  expect(icons.map((i) => i.sizes).sort()).toEqual(['192x192', '512x512', '512x512'])
  expect(icons.filter((i) => i.purpose === 'maskable')).toHaveLength(1)
  for (const icon of icons) {
    const r = await request.get(icon.src)
    expect(r.status(), icon.src).toBe(200)
    expect(r.headers()['content-type'], icon.src).toBe('image/png')
    expect(pngSize(await r.body()), icon.src).toBe(icon.sizes)
  }
})

test('index.html links the manifest and a 180 px apple-touch-icon@1280', journeyAt(1280), async ({ page, request }) => {
  await page.route('**/api/v1/**', (r) => r.fulfill({ status: 404, json: { error: 'no fixture' } }))
  await page.goto('/')
  await expect(page.locator('link[rel="manifest"]')).toHaveAttribute('href', '/manifest.webmanifest')
  const touch = page.locator('link[rel="apple-touch-icon"]')
  const href = await touch.getAttribute('href')
  expect(href).toBe('/icons/apple-touch-icon.png')
  const r = await request.get(href!)
  expect(r.headers()['content-type']).toBe('image/png')
  expect(pngSize(await r.body())).toBe('180x180')
  await expect(page.locator('meta[name="theme-color"]')).toHaveCount(2)
  await expect(page.locator('meta[name="apple-mobile-web-app-capable"]')).toHaveAttribute('content', 'yes')
})
