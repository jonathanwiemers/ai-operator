// Pure helpers of the about line: version compare, link and update note.
import type { About } from './generated/views'

const calendar = /^\d+\.\d+\.\d+$/

// compareVersions: numeric per segment; 0 when either is not YEAR.MONTH.N,
// so a dev build is never older or newer.
export function compareVersions(a: string, b: string): number {
  if (!calendar.test(a) || !calendar.test(b)) return 0
  const x = a.split('.').map(Number)
  const y = b.split('.').map(Number)
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i]! < y[i]! ? -1 : 1
  return 0
}

// aboutLine: the text, its link ('' = unlinked) and the update note.
export function aboutLine(a: About): { text: string; href: string; note: string | null } {
  const r = a.release
  let href = ''
  if (r && r.version === a.version && r.url) href = r.url
  else if (a.releasesURL) href = calendar.test(a.version) ? `${a.releasesURL}/${a.version}` : a.releasesURL
  let note: string | null = null
  if (r && compareVersions(r.version, a.version) > 0) {
    if (r.state === 'Waiting' || r.state === 'Running') note = `Update to ${r.version} rolling out`
    else if (r.state === 'Released') note = `${r.version} is out — reload`
  }
  return { text: `ai-operator ${a.version}`, href, note }
}
