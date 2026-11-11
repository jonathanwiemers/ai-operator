// The mocked API of the browser checks: GET /api/v1/<path> answers the
// fixture of its path. A path missing here answers 404 and fails the check
// that asked for it, so add a fixture when a view starts asking for more.
import { doneMilestones, openMilestones, overview, stream, streamNothingNewer } from './overview'
import { inbox, projects, researches } from './rail'
import { features, me } from './session'

export { now } from './clock'

// A fixture is a JSON body, or a function of the query that returns one.
type Fixture = unknown | ((query: URLSearchParams) => unknown)

export const api: Record<string, Fixture> = {
  '/me': me,
  '/features': features,
  '/projects': projects,
  '/inbox': inbox,
  '/researches': researches,
  '/overview': overview,
  '/milestones': (q: URLSearchParams) => (q.get('state') === 'done' ? doneMilestones : openMilestones),
  '/stream': (q: URLSearchParams) => (q.has('after') ? streamNothingNewer : stream),
}

// answer is the fixture's body for an API path (without /api/v1) and its
// query, undefined without a fixture.
export function answer(path: string, query: URLSearchParams): unknown {
  if (!Object.hasOwn(api, path)) return undefined
  const f = api[path]
  return typeof f === 'function' ? (f as (q: URLSearchParams) => unknown)(query) : f
}
