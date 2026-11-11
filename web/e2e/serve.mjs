// A static server for the built Desk (dist/app) in the browser checks, with
// no dependencies. Like internal/ui/server.go it serves files as they are,
// any other path as index.html (the app routes in the browser), and sends
// the same security headers (securityHeaders). The CSP is read from csp.txt;
// a Go test (TestCSPMatchesBrowserChecks) keeps it equal to the server's.
//
// The API is not served here: the checks answer /api/v1 in the browser from
// fixtures, so a request reaching this server is answered 404.
//
// Usage: node e2e/serve.mjs [port]
import { createServer } from 'node:http'
import { readFile, stat } from 'node:fs/promises'
import { extname, join, normalize, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = fileURLToPath(new URL('.', import.meta.url))
const root = join(here, '..', 'dist', 'app')
const csp = (await readFile(join(here, 'csp.txt'), 'utf8')).trim()
const port = Number(process.argv[2] ?? process.env.PORT ?? 4173)

const types = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.json': 'application/json',
  '.webmanifest': 'application/manifest+json',
  '.woff2': 'font/woff2',
  '.txt': 'text/plain; charset=utf-8',
}

const headers = {
  'Content-Security-Policy': csp,
  'X-Content-Type-Options': 'nosniff',
  'X-Frame-Options': 'DENY',
  'Referrer-Policy': 'same-origin',
}

async function file(name) {
  const p = normalize(join(root, name))
  if (p !== root && !p.startsWith(root + sep)) return null
  try {
    if (!(await stat(p)).isFile()) return null
    return await readFile(p)
  } catch {
    return null
  }
}

function send(res, status, type, body) {
  res.writeHead(status, { ...headers, 'Content-Type': type })
  res.end(body)
}

createServer(async (req, res) => {
  const path = decodeURIComponent(new URL(req.url ?? '/', 'http://localhost').pathname)
  if (path.startsWith('/api/')) return send(res, 404, 'text/plain; charset=utf-8', 'not served by e2e/serve.mjs\n')
  const name = path.replace(/^\/+/, '')
  if (name !== '' && name !== 'index.html') {
    const body = await file(name)
    if (body) return send(res, 200, types[extname(name)] ?? 'application/octet-stream', body)
    if (name.startsWith('assets/')) return send(res, 404, 'text/plain; charset=utf-8', 'not found\n')
  }
  const index = await file('index.html')
  if (!index) return send(res, 404, 'text/plain; charset=utf-8', 'dist/app/index.html is missing: npm run build first\n')
  send(res, 200, types['.html'], index)
}).listen(port, '127.0.0.1', () => console.log(`serving ${root} on http://127.0.0.1:${port}`))
