// The mocked API of the browser checks: GET /api/v1/<path> answers the
// fixture of its path. A path missing here answers 404 and fails the check
// that asked for it, so add a fixture when a view starts asking for more.
// Fixtures live in one module per API area, typed with the generated views.
import { costs, costsMetrics, costsTop } from './costs'
import { answerDecisions } from './decisions'
import { doneMilestones, openMilestones, overview } from './overview'
import { answerPlans, planAPI } from './plans'
import { metricsCode, metricsFlow, metricsPauses } from './metrics'
import { listProblems, problemThread, type ProblemsOpts } from './problems'
import { inbox, projects, projectSets, researches } from './rail'
import { researchAPI } from './research'
import { about, features, me } from './session'
import { answerSpend } from './spend'
import { answerStream, milestones, refundsIssue, shopDetail, shopHistory } from './stream'
import { answerSearch, gitlabStatus, pilotChat, refunds, refundsChat, refundsLogs, refundsResponse, refundsThread } from './task'
import { looseIssues } from './loose'
import { answerTimeline } from './timeline'
import type { MilestoneView } from '../../src/types'

export { now } from './clock'
export { listProblems, problemThread, type ProblemsOpts } from './problems'

// A fixture is a JSON body, or a function of the query that returns one.
type Fixture = unknown | ((query: URLSearchParams) => unknown)

const shopPath = '/projects/team-a/shop'
const refundsPath = `${shopPath}/tasks/${refunds.name}`

export const api: Record<string, Fixture> = {
  '/me': me,
  '/features': features,
  '/about': about,
  // Push is off by default; push.spec.ts answers with a key.
  '/push': { available: false, reason: "Push notifications need the Desk's database (database.host)." },
  '/projects': projects,
  '/projectsets': projectSets,
  '/inbox': inbox,
  '/researches': researches,
  '/overview': overview,
  '/milestones': (q: URLSearchParams) => (q.get('state') === 'done' ? doneMilestones : openMilestones),
  '/stream': answerStream,
  '/spend': answerSpend,
  '/costs': costs,
  '/costs/top': costsTop,
  '/costs/metrics': costsMetrics,
  '/metrics/code': metricsCode,
  '/metrics/flow': metricsFlow,
  '/metrics/pauses': metricsPauses,
  '/gitlab': gitlabStatus,
  '/search': answerSearch,
  '/plans': answerPlans,
  '/timeline': answerTimeline,
  [shopPath]: shopDetail,
  [`${shopPath}/history`]: shopHistory,
  [`${shopPath}/decisions`]: answerDecisions,
  [`${shopPath}/loose-issues`]: looseIssues,
  ...Object.fromEntries(Object.entries(milestones).map(([iid, m]) => [`${shopPath}/milestones/${iid}`, m])),
  [`${shopPath}/milestones/14/issues/281`]: refundsIssue,
  [refundsPath]: refundsResponse,
  [`${refundsPath}/chat`]: refundsChat,
  [`${refundsPath}/thread`]: refundsThread,
  [`${refundsPath}/logs`]: refundsLogs,
  [`${shopPath}/tasks/shop-m14-pilot/chat`]: pilotChat,
  ...researchAPI,
  ...planAPI,
}

const shopProblems = /^\/projects\/team-a\/shop\/milestones\/(\d+)\/problems(?:\/([^/]+))?$/

// answerProblems builds the list or thread for a milestone (and optional
// history override). Specs that replace the milestone in the route mock
// call this with that view so the problems endpoints stay in sync.
export function answerProblems(m: MilestoneView, id: string | undefined, opts?: ProblemsOpts): unknown {
  if (!id) return listProblems(m, opts)
  return problemThread(m, decodeURIComponent(id), opts)
}

// answer is the fixture's body for an API path (without /api/v1) and its
// query, undefined without a fixture.
export function answer(path: string, query: URLSearchParams): unknown {
  const problems = shopProblems.exec(path)
  if (problems) {
    const m = milestones[problems[1]!]
    if (!m) return undefined
    return answerProblems(m, problems[2], query.get('history') === '0' ? { history: false } : undefined)
  }
  if (!Object.hasOwn(api, path)) return undefined
  const f = api[path]
  return typeof f === 'function' ? (f as (q: URLSearchParams) => unknown)(query) : f
}
