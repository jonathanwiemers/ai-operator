import type { ResearchDetail, ResearchForm, ResearchRun } from '../../src/types'
import type { ResearchResponse, ResearchRunsPage } from '../../src/generated/views'
import { ago } from './clock'
import { researches } from './rail'

// The research pages' API: GET /research/form, /researches/{ns}/{name},
// …/runs and …/runs/{id}. payments-news is a scheduled news flash (items),
// checkout-ux-study a one-off report with every block type. Results are
// untrusted agent text with markdown (bold, lists, links).

// GET /research/form: what the one-off form offers.
export const researchForm: ResearchForm = {
  namespace: 'research',
  projects: [
    {
      name: 'shop',
      available: true,
      models: [
        { engine: 'claude', model: 'claude-sonnet-5-5', label: 'claude/sonnet' },
        { engine: 'claude', model: 'claude-opus-5-5', label: 'claude/opus' },
      ],
    },
    {
      name: 'a-project-with-a-rather-long-name.example',
      available: false,
      reason: 'The project has no writer login, so its runs could not store results.',
      models: [],
    },
  ],
  depths: ['quick', 'deep'],
  views: ['newsflash', 'report'],
  efforts: ['low', 'medium', 'high'],
}

const newsDetail: ResearchDetail = {
  ...researches[0]!,
  prompt: `Collect the **payment provider news** of the last day that matter for a European web shop:

- outages and incidents
- API changes and deprecations
- fee changes

Prefer the providers' [status pages](https://status.example.com) and changelogs.`,
  topics: ['Outages', 'API changes', 'Fees'],
  sources: [
    { url: 'https://status.example.com/feed.xml', name: 'Provider status', topic: 'Outages' },
    { url: 'https://developer.example.com/changelog.rss', name: 'Provider changelog', topic: 'API changes' },
  ],
  agent: { engine: 'claude', model: 'claude-sonnet-5-5' },
  run: {
    id: 'run-0042',
    startedAt: ago(60 * 5 + 4),
    finishedAt: ago(60 * 5),
    engine: 'claude',
    model: 'claude-sonnet-5-5',
    outcome: 'succeeded',
    costUSD: '0.41',
  },
}

const reportDetail: ResearchDetail = {
  ...researches[1]!,
  prompt: `Compare the **guest checkout** of five large shops:

1. steps from cart to payment
2. which fields are required
3. how they offer an account afterwards

Link each finding to its source.`,
}

function run(id: string, minutesAgo: number, status: ResearchRun['status'], extra: Partial<ResearchRun> = {}): ResearchRun {
  return {
    id,
    startedAt: ago(minutesAgo + 4),
    finishedAt: status === 'running' ? undefined : ago(minutesAgo),
    status,
    engine: 'claude',
    model: 'claude-sonnet-5-5',
    effort: 'medium',
    costUSD: '0.41',
    choiceReason: 'Quick depth with feeds: **sonnet** is enough, see the research defaults.',
    expiresAt: ago(-60 * 24 * 30),
    ...extra,
  }
}

const newsRuns: ResearchRun[] = [
  run('run-0042', 60 * 5, 'succeeded'),
  run('run-0041', 60 * 29, 'degraded', { warnings: ['The feed **Provider changelog** answered 503; its entries are missing.'] }),
  run('run-0040', 60 * 53, 'failed', { costUSD: '0.08', error: 'The engine stopped: usage limit reached until 08:00.' }),
  run('run-0039', 60 * 77, 'limited', { engine: 'cursor', model: 'auto', costUSD: '' }),
]

// GET /researches/research/payments-news (and its runs/run-0042).
export const newsResponse: ResearchResponse = {
  research: newsDetail,
  run: newsRuns[0]!,
  result: {
    runID: 'run-0042',
    title: 'Payment providers, 10 November',
    summary: 'One **outage** in the EU region and a deprecated refund endpoint; fees unchanged.',
    blocks: [
      {
        type: 'items',
        items: [
          {
            topic: 'Outages',
            title: 'Card payments failed in the EU region for 40 minutes',
            summary: 'Authorisations returned **5xx** between 06:10 and 06:50 UTC. Retries succeeded.',
            url: 'https://status.example.com/incidents/8812',
            source: 'Provider status',
            publishedAt: ago(60 * 6),
            importance: 3,
            new: true,
          },
          {
            topic: 'API changes',
            title: 'The v1 refund endpoint is deprecated from 1 March',
            summary: 'Move to `POST /v2/refunds`; partial refunds then need a **capture id**.',
            url: 'https://developer.example.com/changelog#v2-refunds',
            source: 'Provider changelog',
            publishedAt: ago(60 * 11),
            importance: 2,
          },
          {
            topic: 'Fees',
            title: 'No fee changes announced',
            url: 'https://developer.example.com/pricing',
            importance: 1,
          },
        ],
      },
    ],
    createdAt: ago(60 * 5),
    expiresAt: ago(-60 * 24 * 30),
  },
}

// GET /researches/research/payments-news/runs?limit=…: newest first.
export const newsRunsPage: ResearchRunsPage = { runs: newsRuns }

const reportRun = run('run-0007', 60 * 26, 'succeeded', { effort: 'high', model: 'claude-opus-5-5', costUSD: '2.95' })

// GET /researches/research/checkout-ux-study (and its runs/run-0007).
export const reportResponse: ResearchResponse = {
  research: reportDetail,
  run: reportRun,
  result: {
    runID: 'run-0007',
    title: 'Guest checkout in five large shops',
    summary: 'All five offer a **guest checkout**; three ask for an account only after payment.',
    blocks: [
      { type: 'heading', text: 'Findings', level: 2 },
      {
        type: 'markdown',
        text: `Most shops keep the guest checkout to **three steps**:

- address and delivery
- payment
- confirmation, with an *optional* account

Two shops ask for a phone number; see [the study notes](https://research.example.com/checkout/notes).`,
      },
      {
        type: 'stats',
        stats: [
          { label: 'Median steps', value: '3' },
          { label: 'Required fields', value: '9', delta: '-2 vs. 2025' },
          { label: 'Account offered after payment', value: '3 of 5' },
        ],
      },
      {
        type: 'table',
        columns: ['Shop', 'Steps', 'Required fields', 'Account offer'],
        rows: [
          ['shop-one.example', '3', '8', 'after payment'],
          ['shop-two.example', '4', '11', 'before payment'],
          ['a-shop-with-a-rather-long-domain-name.example', '3', '9', 'after payment, as a checkbox on the confirmation page'],
        ],
      },
      { type: 'heading', text: 'Shops in detail', level: 3 },
      {
        type: 'items',
        items: [
          {
            title: 'shop-two.example asks for an account before payment, with a skip link below the fold',
            summary: 'The **guest** option is a text link under the sign-in form.',
            url: 'https://research.example.com/checkout/shop-two',
            source: 'Study notes',
            importance: 3,
          },
          {
            title: 'a-shop-with-a-rather-long-domain-name.example keeps nine required fields',
            url: 'https://research.example.com/checkout/long-domain',
            importance: 2,
          },
        ],
      },
      {
        type: 'links',
        links: [
          { title: 'Baymard: checkout usability', url: 'https://research.example.com/baymard/checkout' },
          { title: 'Study notes', url: 'https://research.example.com/checkout/notes' },
        ],
      },
    ],
    createdAt: ago(60 * 26),
    expiresAt: ago(-60 * 24 * 30),
  },
}

// The research API by path (without /api/v1).
export const researchAPI: Record<string, unknown> = {
  '/research/form': researchForm,
  '/researches/research/payments-news': newsResponse,
  '/researches/research/payments-news/runs': newsRunsPage,
  '/researches/research/payments-news/runs/run-0042': newsResponse,
  '/researches/research/checkout-ux-study': reportResponse,
  '/researches/research/checkout-ux-study/runs': { runs: [reportRun] } satisfies ResearchRunsPage,
  '/researches/research/checkout-ux-study/runs/run-0007': reportResponse,
}
