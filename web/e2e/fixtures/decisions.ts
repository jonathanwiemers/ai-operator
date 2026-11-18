import type { DecisionsResponse, DecisionView } from '../../src/generated/views'
import { ago } from './clock'
import { payments, refundsTitle, shopURL } from './task'

// GET /projects/team-a/shop/decisions (the Decisions tab, R11-R13):
// milestone %14 has two implementer decisions of merged #280 and two
// active decisions of #281, one from the plan. D280-1 was revisited twice:
// the follow-up #281 (open) applied the first request with D281-2, and a
// comment on #281 asks to revisit D281-2 (not applied yet), so D280-1's
// chain has two steps. The revisit modes as the server sets them for an
// owner: D280-1 none (replaced), D280-2 prompt (a closed issue, to the
// pilot), #281's comment (an open issue with its implement task). The
// answer filters by ?q= and ?state= as the server does; other milestones
// have none.

const noteURL = (issue: number, note: number) => `${shopURL}/-/issues/${issue}#note_${note}`
const comment = (id: string) => ({
  mode: 'comment' as const,
  text: `Comments on #281. Its next run changes the code and posts a decision that supersedes ${id}.`,
  task: 'shop-281-implement',
  command: `/agent revisit ${id} `,
})

const issue280 = { iid: 280, title: 'Provider SDK 4 with refund support', state: 'closed' }
const issue281 = { iid: 281, title: refundsTitle, state: 'opened', task: 'shop-281-implement' }

export const shopDecisions: DecisionView[] = [
  {
    id: 'D280-1',
    kind: 'implementer',
    title: 'Refund currency',
    question: 'Which currency does a refund use when the provider converted the payment?',
    choice: 'The **settlement** currency of the provider',
    why: 'The SDK refunds in the settlement currency **only**. <span onclick="alert(1)">raw</span> stays text.',
    alternatives: ['The order currency', 'Ask the support agent per refund'],
    runID: 'shop-280-implement#2',
    url: noteURL(280, 9101),
    postedAt: ago(60 * 26),
    milestoneIID: payments.iid,
    issue: issue280,
    superseded: true,
    supersededBy: 'D281-2',
    revisits: [
      {
        source: 'followUp',
        requestedBy: 'Jo via the Desk',
        instruction: 'Refund in the **order** currency <img src=x onerror="alert(1)">',
        followUpIID: 281,
        followUpState: 'opened',
        at: ago(60 * 5),
        applied: true,
      },
    ],
    revisit: { mode: 'none', text: 'Already replaced by D281-2.' },
  },
  {
    id: 'D280-2',
    kind: 'implementer',
    title: 'Refund retries',
    question: 'How often does a failed refund call retry?',
    choice: 'Three times with backoff',
    why: 'The provider limits refund calls per minute.',
    alternatives: ['Once', 'Until it succeeds'],
    runID: 'shop-280-implement#2',
    url: noteURL(280, 9102),
    postedAt: ago(60 * 26),
    milestoneIID: payments.iid,
    issue: issue280,
    superseded: false,
    revisits: [],
    revisit: {
      mode: 'prompt',
      text:
        'Opens a revisit prompt. Your choice creates a follow-up issue in this milestone, queued after the planned issues. ' +
        'D280-2 is marked superseded when the follow-up posts its new decision.',
      task: 'shop-m14-pilot',
      command: '/agent revisit D280-2 ',
    },
  },
  {
    id: 'D281-P1',
    kind: 'planner',
    title: 'Where refunds start',
    question: 'Where does support start a refund?',
    choice: 'From the order page in the back office',
    why: 'Support already works there; a separate page would split the history.',
    alternatives: [],
    runID: 'shop-m14-spec#1',
    url: noteURL(281, 9050),
    postedAt: ago(60 * 30),
    milestoneIID: payments.iid,
    issue: issue281,
    superseded: false,
    revisits: [],
    revisit: comment('D281-P1'),
  },
  {
    id: 'D281-2',
    kind: 'implementer',
    title: 'Refund in the order currency',
    question: 'Which currency does a refund use, now that SDK 4 converts back?',
    choice: 'The order currency, converted back by the provider',
    why: 'The customer gets back what they paid, in their currency.',
    alternatives: ['The settlement currency (D280-1)'],
    runID: 'shop-281-implement#1',
    url: noteURL(281, 9180),
    postedAt: ago(90),
    milestoneIID: payments.iid,
    issue: issue281,
    supersedes: 'D280-1',
    superseded: false,
    revisits: [
      {
        source: 'comment',
        requestedBy: 'sam',
        instruction: 'Ask support when the provider cannot convert back',
        url: noteURL(281, 9200),
        at: ago(30),
        applied: false,
      },
    ],
    revisit: comment('D281-2'),
  },
]

export function answerDecisions(q: URLSearchParams): DecisionsResponse {
  const milestone = Number(q.get('milestone') ?? 0)
  const search = (q.get('q') ?? '').toLowerCase()
  const state = q.get('state') ?? 'all'
  const decisions = shopDecisions.filter(
    (d) =>
      (!milestone || d.milestoneIID === milestone) &&
      (state === 'all' || (state === 'superseded') === d.superseded) &&
      (!search ||
        [d.id, d.title, d.question, d.choice, d.why, ...d.alternatives].some((s) => s.toLowerCase().includes(search))),
  )
  return { available: true, decisions }
}
