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
