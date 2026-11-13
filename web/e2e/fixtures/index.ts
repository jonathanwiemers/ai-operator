// The mocked API of the browser checks: GET /api/v1/<path> answers the
// fixture of its path. A path missing here answers 404 and fails the check
// that asked for it, so add a fixture when a view starts asking for more.
// Fixtures live in one module per API area, typed with the generated views.
import { costs, costsTop } from './costs'
import { doneMilestones, openMilestones, overview } from './overview'
import { inbox, projects, researches } from './rail'
import { researchAPI } from './research'
import { about, features, me } from './session'
import { answerSpend } from './spend'
import { answerStream, milestones, refundsIssue, shopDetail, shopHistory } from './stream'
import { answerSearch, gitlabStatus, pilotChat, refunds, refundsChat, refundsResponse, refundsThread } from './task'

export { now } from './clock'

// A fixture is a JSON body, or a function of the query that returns one.
type Fixture = unknown | ((query: URLSearchParams) => unknown)

const shopPath = '/projects/team-a/shop'
const refundsPath = `${shopPath}/tasks/${refunds.name}`

export const api: Record<string, Fixture> = {
  '/me': me,
  '/features': features,
  '/about': about,
  '/projects': projects,
  '/inbox': inbox,
  '/researches': researches,
  '/overview': overview,
  '/milestones': (q: URLSearchParams) => (q.get('state') === 'done' ? doneMilestones : openMilestones),
  '/stream': answerStream,
  '/spend': answerSpend,
  '/costs': costs,
  '/costs/top': costsTop,
  '/gitlab': gitlabStatus,
  '/search': answerSearch,
  [shopPath]: shopDetail,
  [`${shopPath}/history`]: shopHistory,
  ...Object.fromEntries(Object.entries(milestones).map(([iid, m]) => [`${shopPath}/milestones/${iid}`, m])),
  [`${shopPath}/milestones/14/issues/281`]: refundsIssue,
  [refundsPath]: refundsResponse,
  [`${refundsPath}/chat`]: refundsChat,
  [`${refundsPath}/thread`]: refundsThread,
  [`${shopPath}/tasks/shop-m14-pilot/chat`]: pilotChat,
  ...researchAPI,
}

// answer is the fixture's body for an API path (without /api/v1) and its
// query, undefined without a fixture.
export function answer(path: string, query: URLSearchParams): unknown {
  if (!Object.hasOwn(api, path)) return undefined
  const f = api[path]
  return typeof f === 'function' ? (f as (q: URLSearchParams) => unknown)(query) : f
}
