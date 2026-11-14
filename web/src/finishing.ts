import type { FinishingEntry } from './generated/views'

// What holds a done milestone open on GitLab, one line per hold (R7): href on
// GitLab, retro to the milestone's Retrospective tab, links to companions.
export type HoldLine = { text: string; href?: string; retro?: true; links?: { text: string; href: string }[] }

// companionURL: "group/lib!7" on gitlab (the base URL) is its merge request.
export function companionURL(gitlab: string, ref: string): string {
  const [project, mr] = ref.split('!')
  return `${gitlab.replace(/\/+$/, '')}/${project}${mr ? `/-/merge_requests/${mr}` : ''}`
}

export function holdLines(e: FinishingEntry, gitlab: string): HoldLine[] {
  const out: HoldLine[] = []
  if (e.replanning) out.push({ text: 'Re-planning' })
  if (e.retroWriting) out.push({ text: 'Retrospective being written' })
  if (e.followUps) out.push({ text: 'Follow-ups to settle', retro: true })
  for (const i of e.openIssues ?? []) out.push({ text: `Open: #${i.iid}${i.title ? ` ${i.title}` : ''}`, href: i.webURL })
  const cs = e.companions ?? []
  if (cs.length) out.push({ text: 'Companions not landed:', links: cs.map((c) => ({ text: c, href: companionURL(gitlab, c) })) })
  return out.length ? out : [{ text: 'Closes on the next poll' }]
}
