import type { PilotActed } from './generated/api'
import type { MilestoneView, Pause, PromptView } from './types'

// Groups a milestone's prompts, the pilot's own actions and its pauses into
// problems, one per issue IID (0: the milestone), for the Handled section of
// the Needs you tab. Pure: no Vue, no DOM.

export type NeedsEntry =
  | { kind: 'prompt'; at: string; prompt: PromptView }
  | { kind: 'acted'; at: string; acted: PilotActed }
  | { kind: 'pause'; at: string; pause: Pause }

export interface Problem {
  iid: number
  title: string
  open: boolean
  entries: NeedsEntry[]
  attempts?: { used: number; max: number }
}

// problemAnchor is the element id of a problem; links point to it.
export function problemAnchor(iid: number): string {
  return `problem-${iid}`
}

const ms = (s: string): number => {
  const t = Date.parse(s)
  return Number.isNaN(t) ? 0 : t
}

export function needsCases(m: MilestoneView): Problem[] {
  const byIID = new Map<number, Problem>()
  const get = (iid: number): Problem => {
    let p = byIID.get(iid)
    if (!p) {
      const title = iid === 0 ? '' : (m.issues ?? []).find((i) => i.iid === iid)?.title ?? ''
      p = { iid, title, open: false, entries: [] }
      byIID.set(iid, p)
    }
    return p
  }

  for (const prompt of m.prompts ?? []) {
    const p = get(prompt.issueIID ?? 0)
    if (prompt.state === 'open') p.open = true
    p.entries.push({ kind: 'prompt', at: prompt.closedAt ?? prompt.postedAt ?? '', prompt })
  }
  for (const acted of m.pilot?.acted ?? []) {
    for (const iid of new Set(acted.issues ?? [])) get(iid).entries.push({ kind: 'acted', at: acted.at, acted })
  }
  for (const pause of m.pauses?.pauses ?? []) {
    get(pause.item ?? 0).entries.push({ kind: 'pause', at: pause.until ?? pause.since, pause })
  }

  const ap = m.autopilot
  if (ap?.phase === 'Paused') get(ap.pausedFor ?? 0).open = true
  for (const h of ap?.holds ?? []) get(h.iid).open = true

  const interventions = m.pilot?.interventions ?? []
  if (m.pilot) {
    const max = interventions[0]?.max
    for (const p of byIID.values()) {
      const iv = interventions.find((i) => i.issueIID === p.iid)
      if (iv) p.attempts = { used: iv.used, max: iv.max }
      else if (max !== undefined) p.attempts = { used: 0, max }
    }
  }

  const newest = (p: Problem): number => p.entries[0] ? ms(p.entries[0].at) : 0
  const out: Problem[] = []
  for (const p of byIID.values()) {
    p.entries.sort((a, b) => ms(b.at) - ms(a.at))
    if (p.entries.length > 0 || p.open || (p.attempts?.used ?? 0) > 0) out.push(p)
  }
  return out.sort((a, b) => Number(b.open) - Number(a.open) || newest(b) - newest(a) || a.iid - b.iid)
}
