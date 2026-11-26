// Files attached to a plan chat message (milestone "Desk plans: attach
// screenshots and files", Decisions 2, 4 and 5): the browser's checks
// before upload, the upload on Send and the message body that carries
// GitLab's links. The limits and the extensions are a copy of Go's
// internal/attachments; its web_test.go fails when they drift.

export const maxFileBytes = 10 * 1024 * 1024
export const maxFiles = 5
export const allowedExtensions = ['.png', '.jpg', '.jpeg', '.gif', '.webp', '.pdf', '.txt', '.log', '.md', '.json', '.yaml', '.yml', '.csv']

// maxBodyChars is the server's limit of a chat message, in code points.
const maxBodyChars = 16 * 1024

// accept is the file dialog's filter.
export const accept = allowedExtensions.join(',')

export interface Refused {
  name: string
  reason: string
}

function extension(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot).toLowerCase() : ''
}

// checkFiles adds the added files that pass the type, size and count rules
// to current, in order, and says why each other one is refused.
export function checkFiles(current: File[], added: File[]): { files: File[]; refused: Refused[] } {
  const files = [...current]
  const refused: Refused[] = []
  for (const f of added) {
    let reason = ''
    if (!allowedExtensions.includes(extension(f.name))) reason = 'only PNG, JPG, GIF, WebP, PDF and text files'
    else if (f.size > maxFileBytes) reason = 'larger than 10 MiB'
    else if (f.size === 0) reason = 'the file is empty'
    else if (files.length >= maxFiles) reason = `at most ${maxFiles} files per message`
    if (reason) refused.push({ name: f.name, reason: `${f.name}: ${reason}` })
    else files.push(f)
  }
  return { files, refused }
}

// pastedName names the n-th clipboard image without a usable name.
export function pastedName(n: number): string {
  return `pasted-${n}.png`
}

// pastedFiles are the files of a paste, a PNG without a name or with the
// generic one browsers give a copied screenshot ("image.png") renamed with
// pastedName from next on.
export function pastedFiles(list: File[], next: number): File[] {
  return list.map((f) => {
    if (f.type !== 'image/png' || (f.name && f.name !== 'image.png')) return f
    return new File([f], pastedName(next++), { type: 'image/png', lastModified: f.lastModified })
  })
}

// fileSize is a size as the chips show it.
export function fileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

// withAttachments is the message: the owner's text, then one line per file.
export function withAttachments(body: string, markdown: string[]): string {
  return [body.trim(), ...markdown].filter(Boolean).join('\n')
}

// bodyTooLong is the server's 16 KiB rule, counted in code points.
export function bodyTooLong(body: string): boolean {
  return [...body].length > maxBodyChars
}

// uploadError is the error of a failed upload of the file named name: a
// thrown ApiError (status and message) or any other error.
function uploadError(e: unknown, name: string): string {
  const status = (e as { status?: unknown } | null)?.status
  if (status === 409) return 'Connect your GitLab account to attach files.'
  if (status === 429) return `${name}: too many uploads; try again in a minute.`
  const msg = e instanceof Error ? e.message : String(e)
  return msg.includes(name) ? msg : `${name}: ${msg}`
}

// uploadAll uploads the files one by one, in order, and stops at the first
// failure: its error names the file, status is the HTTP status if any.
export async function uploadAll(
  files: File[],
  upload: (f: File) => Promise<{ markdown: string }>,
): Promise<{ markdown: string[] } | { error: string; file: string; status?: number }> {
  const markdown: string[] = []
  for (const f of files) {
    try {
      markdown.push((await upload(f)).markdown)
    } catch (e) {
      const status = (e as { status?: unknown } | null)?.status
      return { error: uploadError(e, f.name), file: f.name, ...(typeof status === 'number' ? { status } : {}) }
    }
  }
  return { markdown }
}

// UploadProject is the project whose GitLab uploads count as attachments
// (Decision 9): url is the GitLab base URL, path the project's path with
// namespace. The /-/project/<id>/ form counts only with id; the relative
// form, which GitLab returns, always counts.
export type UploadProject = { url: string; path: string; id?: number }

// UploadLink is an upload link found in text, as Go's attachments.Link.
export interface UploadLink {
  raw: string
  secret: string
  filename: string
  image: boolean
}

// A markdown link or image with an optional title (groups 1 and 2: the "!"
// and the URL), or a bare http(s) URL: Go's linkRE.
const uploadLinkRe = /(!?)\[[^\]\n]*\]\(\s*([^\s()]+)(?:\s+"[^"\n]*")?\s*\)|https?:\/\/[^\s<>()[\]"'`]+/g

function uploadPrefixes(p: UploadProject | undefined): string[] {
  const out = ['/uploads/']
  const base = (p?.url ?? '').replace(/\/+$/, '')
  if (!base) return out
  const path = (p?.path ?? '').replace(/^\/+|\/+$/g, '')
  if (path) out.push(`${base}/${path}/uploads/`)
  if (p?.id) out.push(`${base}/-/project/${p.id}/uploads/`)
  return out
}

// parseUpload returns the secret and the decoded file name of raw when it is
// an upload link under one of prefixes: a 32-hex secret and one harmless
// path segment.
function parseUpload(raw: string, prefixes: string[]): { secret: string; filename: string } | null {
  const pre = prefixes.find((x) => raw.length > x.length && raw.slice(0, x.length).toLowerCase() === x.toLowerCase())
  if (!pre) return null
  const rest = raw.slice(pre.length)
  const slash = rest.indexOf('/')
  if (slash < 0) return null
  const secret = rest.slice(0, slash)
  const file = rest.slice(slash + 1)
  if (!/^[0-9a-f]{32}$/i.test(secret) || /[/?#]/.test(file)) return null
  let filename: string
  try {
    filename = decodeURIComponent(file)
  } catch {
    return null
  }
  if (!filename || filename === '.' || filename === '..' || /[/\\]/.test(filename)) return null
  return { secret: secret.toLowerCase(), filename }
}

// uploadMatches are the upload links of p in text with where each was
// written (the whole markdown link or the bare URL), duplicates included.
function uploadMatches(text: string, p: UploadProject | undefined): { link: UploadLink; start: number; end: number }[] {
  const prefixes = uploadPrefixes(p)
  const out: { link: UploadLink; start: number; end: number }[] = []
  for (const m of text.matchAll(uploadLinkRe)) {
    const start = m.index
    let end = start + m[0].length
    let raw = m[2]
    if (raw === undefined) {
      raw = m[0].replace(/[.,;:!?]+$/, '')
      end = start + raw.length
    }
    const up = parseUpload(raw, prefixes)
    if (up) out.push({ link: { raw, ...up, image: m[1] === '!' }, start, end })
  }
  return out
}

// findUploads is the web twin of Go's attachments.Find: the upload links of
// p in text, in order, each secret and file name once.
export function findUploads(text: string, p?: UploadProject): UploadLink[] {
  const seen = new Set<string>()
  const out: UploadLink[] = []
  for (const { link } of uploadMatches(text, p)) {
    const key = `${link.secret}/${link.filename}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(link)
  }
  return out
}

// uploadHref is the Desk route that serves an upload with the viewer's
// GitLab token.
export function uploadHref(namespace: string, name: string, link: { secret: string; filename: string }): string {
  const e = encodeURIComponent
  return `/api/v1/projects/${e(namespace)}/${e(name)}/plans/uploads/${link.secret}/${e(link.filename)}`
}

export interface ShownFile {
  name: string
  href: string
  image: boolean
}

const imageExtensions = ['.png', '.jpg', '.jpeg', '.gif', '.webp']
// Marks where a link was cut out; chat bodies cannot hold NUL (Postgres).
const cut = '\u0000'

// splitAttachments is a plan chat message as the Desk shows it (Decision
// 7): the text without its upload links, and the files below it, each
// once. A line left empty by a removed link goes; everything else,
// including other projects' links, stays text. A file is an image by its
// extension, whether or not it was written as ![..](..).
export function splitAttachments(
  body: string,
  p: UploadProject | undefined,
  href: (link: UploadLink) => string,
): { text: string; files: ShownFile[] } {
  const matches = uploadMatches(body, p)
  if (!matches.length) return { text: body, files: [] }
  let text = ''
  let at = 0
  for (const m of matches) {
    text += body.slice(at, m.start) + cut
    at = m.end
  }
  text += body.slice(at)
  const lines: string[] = []
  for (const line of text.split('\n')) {
    if (!line.includes(cut)) {
      lines.push(line)
      continue
    }
    const left = line.replace(/[ \t]*\u0000+[ \t]*/g, (s) => (/^[ \t]/.test(s) && /[ \t]$/.test(s) ? ' ' : '')).trimEnd()
    if (left.trim()) lines.push(left)
  }
  const files = findUploads(body, p).map((l) => ({
    name: l.filename,
    href: href(l),
    image: imageExtensions.includes(extension(l.filename)),
  }))
  return { text: lines.join('\n').trim(), files }
}

// PlanUploads is what a plan page knows to show its attachments: the plan's
// project, its GitLab ref (when the project summary has one), and whether
// the viewer's GitLab is connected (unknown while loading: files show as
// chips, nothing is fetched), else where to connect it.
export interface PlanUploads {
  namespace: string
  name: string
  project?: UploadProject
  connected?: boolean
  connect?: string
}

// uploadRewrite is Markdown's uploads option for a plan: an upload link of
// the plan's project points at the Desk route.
export function uploadRewrite(u: PlanUploads): (href: string) => string | undefined {
  const prefixes = uploadPrefixes(u.project)
  return (raw) => {
    const up = parseUpload(raw, prefixes)
    return up ? uploadHref(u.namespace, u.name, up) : undefined
  }
}
