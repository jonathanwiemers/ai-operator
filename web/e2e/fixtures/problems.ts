// Problem list and thread fixtures for GET …/milestones/:iid/problems and
// …/problems/:id (spec desk-problem-thread). Built from a MilestoneView so
// overridden milestones in the specs keep matching open prompts, pauses and
// holds. History true by default: open rows plus a few resolved ones and a
// short timeline; history false is the CRD-only case (Decision 13).
import type { Facts } from '../../src/generated/problem'
import { needsOpen } from '../../src/needsOpen'
import { splitPrompts } from '../../src/promptCard'
import type {
  MilestoneView,
  ProblemList,
  ProblemThread,
  ProblemView,
  PromptView,
  StreamEvent,
} from '../../src/types'
import { ago } from './clock'

export type ProblemsOpts = { history?: boolean }

// listProblems: open problems from the CRD grouping, then resolved ones when
// history is on (open first, as the server sorts them).
export function listProblems(m: MilestoneView, opts: ProblemsOpts = {}): ProblemList {
  const history = opts.history !== false
  const open = openViews(m, history)
  const openIids = new Set(open.map((p) => p.issueIID))
  const resolved = history ? resolvedViews(m, openIids) : []
  return { problems: [...open, ...resolved], history }
}

// problemThread answers …/problems/:id (row id or issue-<iid>). Undefined
// when that problem is not on the milestone.
export function problemThread(m: MilestoneView, id: string, opts: ProblemsOpts = {}): ProblemThread | undefined {
  const list = listProblems(m, opts)
  const problem = findProblem(list.problems, id)
  if (!problem) return undefined
  const iid = problem.issueIID
  const { open, closed } = splitPrompts(m.prompts)
  const ofIssue = (p: PromptView) => (p.issueIID ?? 0) === iid
  const openFor = open.filter(ofIssue)
  // Newest open question first (postedAt descending); the bar shows one.
  openFor.sort((a, b) => Date.parse(b.postedAt ?? '') - Date.parse(a.postedAt ?? ''))
  const prompt = problem.state === 'open' ? openFor[0] : undefined
  const facts = (m.problems?.find((f) => f.iid === iid) ?? undefined) as Facts | undefined
  const history = list.history
  const pilot = m.pilot?.task
  const permissions = m.permissions ?? {
    gitlab: true,
    steer: true,
    chat: true,
    transcript: true,
    merge: true,
    parallel: true,
    draft: true,
    followUp: true,
    dismiss: true,
  }
  const exits =
    problem.state === 'open' && !prompt
      ? (m.autopilot?.holds?.find((h) => h.iid === iid)?.exits ??
        (needsOpen(m).problems.find((p) => p.iid === iid)?.paused ? m.autopilot?.exits : undefined) ??
        [])
      : []
  return {
    problem,
    facts,
    prompt,
    prompts: history ? [] : closed.filter(ofIssue),
    exits,
    events: history ? threadEvents(m, iid) : [],
    history,
    pilot,
    permissions: { ...permissions, chat: !!permissions.chat && !!pilot },
  }
}

function openViews(m: MilestoneView, history: boolean): ProblemView[] {
  return needsOpen(m).problems.map((p) => ({
    id: history ? 1000 + p.iid : 0,
    issueIID: p.iid,
    title: p.title ?? (p.iid ? m.issues?.find((i) => i.iid === p.iid)?.title : undefined),
    state: 'open',
    status: 'waiting',
    cause: causeOf(m, p.iid),
    openedAt: ago(30),
  }))
}

// Resolved rows the Handled section used to list: issues with closed
// prompts or past pauses/acts that are not open now, plus the milestone
// when it had questions of its own.
function resolvedViews(m: MilestoneView, openIids: Set<number>): ProblemView[] {
  const iids = new Set<number>()
  for (const p of m.prompts ?? []) {
    if (p.state && p.state !== 'open') iids.add(p.issueIID ?? 0)
  }
  for (const pause of m.pauses?.pauses ?? []) {
    if (pause.until) iids.add(pause.item ?? 0)
  }
  for (const a of m.pilot?.acted ?? []) {
    for (const issue of a.issues ?? []) iids.add(issue)
  }
  return [...iids]
    .filter((iid) => !openIids.has(iid))
    .sort((a, b) => b - a)
    .map((iid) => ({
      id: 2000 + iid,
      issueIID: iid,
      title: iid ? m.issues?.find((i) => i.iid === iid)?.title : m.title,
      state: 'resolved' as const,
      status: 'resolved' as const,
      cause: causeOf(m, iid) || (iid === 0 ? 'Answered' : 'Resolved'),
      openedAt: ago(60),
      resolvedAt: ago(20),
      resolvedBy: 'owner',
    }))
}

function causeOf(m: MilestoneView, iid: number): string {
  const facts = m.problems?.find((f) => f.iid === iid)
  if (facts?.run?.cause) return facts.run.cause
  const hold = m.autopilot?.holds?.find((h) => h.iid === iid)
  if (hold?.reason) return hold.reason
  const open = needsOpen(m).problems.find((p) => p.iid === iid)
  if (open?.paused && m.autopilot?.pausedReason) return m.autopilot.pausedReason
  return ''
}

function findProblem(problems: ProblemView[], id: string): ProblemView | undefined {
  if (id.startsWith('issue-')) {
    const iid = Number(id.slice('issue-'.length))
    if (!Number.isFinite(iid)) return undefined
    return problems.find((p) => p.issueIID === iid)
  }
  const n = Number(id)
  if (!Number.isFinite(n) || n <= 0) return undefined
  return problems.find((p) => p.id === n)
}

// A short timeline per issue: chat about the problem, a pilot act, and an
// open/resolve pair where it fits. Newest first.
function threadEvents(m: MilestoneView, iid: number): StreamEvent[] {
  const ns = 'team-a'
  const project = 'shop'
  const mid = m.iid
  const base = { namespace: ns, project, milestoneIID: mid, issueIID: iid || undefined }
  if (iid === 558) {
    return [
      ev(910, ago(5), 'chat.agent', 'Looking at the saved WIP and the decode error.', {
        ...base,
        task: m.pilot?.task,
        data: { body: 'I can retry #558 with a hint to answer in JSON.', role: 'agent', kind: 'message', authorName: 'pilot' },
      }),
      ev(909, ago(6), 'chat.user', 'problem: #558\n\nretry with a JSON hint', {
        ...base,
        task: m.pilot?.task,
        actor: 'jo',
        data: { body: 'problem: #558\n\nretry with a JSON hint', role: 'user', kind: 'message', authorName: 'Jo' },
      }),
      ev(908, ago(15), 'pilot.acted', 'Restart the run of #558', {
        ...base,
        data: { why: 'The decode failed; the WIP is saved.', lines: ['- **restartRun** #558: started run 2'], ran: true },
      }),
      ev(907, ago(30), 'problem.opened', 'Problem opened', {
        ...base,
        data: { cause: causeOf(m, 558) },
      }),
    ]
  }
  if (iid === 281) {
    return [
      ev(820, ago(10), 'chat.agent', 'Partial refunds need a decision.', {
        ...base,
        task: m.pilot?.task,
        data: { body: 'I posted the question on the task page.', role: 'agent', kind: 'message', authorName: 'pilot' },
      }),
      ev(819, ago(12), 'chat.user', 'problem: #281\n\nwhat blocks the run?', {
        ...base,
        task: m.pilot?.task,
        actor: 'jo',
        data: { body: 'problem: #281\n\nwhat blocks the run?', role: 'user', kind: 'message', authorName: 'Jo' },
      }),
      ev(818, ago(24), 'autopilot.paused', 'Paused for an answer', { ...base, data: { reason: m.autopilot?.pausedReason } }),
      ev(817, ago(30), 'problem.opened', 'Problem opened', { ...base, data: { cause: causeOf(m, 281) } }),
    ]
  }
  if (iid === 280) {
    return [
      ev(710, ago(25), 'pilot.acted', 'Restart the run of #280', {
        ...base,
        data: { why: 'The retried job failed again.', lines: ['- **restartRun** #280: started run 3'], ran: true },
      }),
      ev(709, ago(26), 'hold.cleared', 'Hold cleared', { ...base, data: { holdKind: 'ciRed' } }),
      ev(708, ago(40), 'problem.resolved', 'Problem resolved', {
        ...base,
        data: { resolvedBy: 'owner' },
      }),
      ev(707, ago(60), 'problem.opened', 'Problem opened', { ...base, data: { cause: 'CI red' } }),
    ]
  }
  if (iid === 0) {
    return [
      ev(610, ago(7), 'prompt.answered', 'Q3 answered', {
        ...base,
        data: { promptID: 'Q3', optionLabel: 'Release SDK 4 first', by: 'jo' },
      }),
      ev(609, ago(8), 'prompt.posted', 'Q3 asked', { ...base, data: { promptID: 'Q3' } }),
    ]
  }
  return [
    ev(500 + iid, ago(20), 'problem.opened', 'Problem opened', {
      ...base,
      data: { cause: causeOf(m, iid) },
    }),
  ]
}

function ev(
  id: number,
  at: string,
  kind: StreamEvent['kind'],
  summary: string,
  rest: Partial<StreamEvent> & { data: StreamEvent['data'] },
): StreamEvent {
  return { namespace: rest.namespace!, project: rest.project!, ...rest, id, at, kind, summary }
}
