// Renders public/logo.svg to the PNG icons the manifest and index.html name
// (public/icons/). Run it by hand, `node scripts/icons.mjs`, after
// public/logo.svg or the light theme's --surface colour changes, and commit
// the PNGs. It is not part of the build. Needs Chromium:
// `npx playwright install chromium`.
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from '@playwright/test'

const here = fileURLToPath(new URL('.', import.meta.url))
const out = join(here, '..', 'public', 'icons')
const svg = await readFile(join(here, '..', 'public', 'logo.svg'), 'utf8')
const theme = '#ffffff' // --surface of the light theme (src/style.css)

// size: the PNG's side; pad: share of each side left free around the logo;
// bg: the opaque background, or null for transparent corners.
const icons = [
  { file: 'apple-touch-icon.png', size: 180, pad: 0, bg: theme },
  { file: 'icon-192.png', size: 192, pad: 0, bg: null },
  { file: 'icon-512.png', size: 512, pad: 0, bg: null },
  { file: 'icon-512-maskable.png', size: 512, pad: 0.2, bg: theme },
]

await mkdir(out, { recursive: true })
const browser = await chromium.launch()
try {
  for (const { file, size, pad, bg } of icons) {
    const page = await browser.newPage({ viewport: { width: size, height: size } })
    const inner = Math.round(size * (1 - 2 * pad))
    const url = `data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}`
    await page.setContent(
      `<body style="margin:0;width:${size}px;height:${size}px;display:grid;place-items:center;background:${bg ?? 'transparent'}">` +
        `<img src="${url}" width="${inner}" height="${inner}"></body>`,
    )
    await writeFile(join(out, file), await page.screenshot({ omitBackground: bg === null }))
    await page.close()
  }
} finally {
  await browser.close()
}
