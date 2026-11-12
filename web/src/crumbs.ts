import type { RouteName } from './routeNames.ts'

// The breadcrumbs of a route (R19, decisions 16 and 17): Overview › project
// › %milestone › #issue, the task page under its issue when it has one, and
// Research › research › runs › run. The last crumb names the current view
// and has no link; every other one links to its level without ?tab=, so it
// opens that level's default tab. Names come from crumbNames, filled by
// the views from what they loaded; until then a crumb shows the IID or
// the route's label.

export interface CrumbLink {
  name: RouteName
  params: Record<string, string>
}

export interface Crumb {
  label: string
  to?: CrumbLink
}

// CrumbNames are the titles the route does not carry. Each one names the
// thing it belongs to, so a name left over from another view never shows.
export interface CrumbNames {
  milestone?: { namespace: string; name: string; iid: number; title: string }
  issue?: { namespace: string; name: string; iid: number; title: string }
  // task: the task's milestone and issue IIDs (an Implement task has both).
  task?: { namespace: string; name: string; task: string; milestone?: number; issue?: number }
  run?: { namespace: string; name: string; id: string; title: string }
}

type ParamValue = string | string[] | undefined

export interface CrumbRoute {
  name: RouteName
  params: Record<string, ParamValue>
  // query is the route's; no crumb links to it (the tab stays out).
  query?: Record<string, unknown>
}

export const overviewLabel = 'Overview'
export const researchLabel = 'Research'

// milestoneLabel is a milestone's title, else %<iid>.
export function milestoneLabel(iid: number, title?: string): string {
  return title || `%${iid}`
}

// issueLabel is an issue's title, else #<iid>.
export function issueLabel(iid: number, title?: string): string {
  return title || `#${iid}`
}

function param(params: Record<string, ParamValue>, key: string): string {
  const v = params[key]
  return (Array.isArray(v) ? v[0] : v) ?? ''
}

function iidOf(s: string): number | undefined {
  return /^[1-9][0-9]*$/.test(s) && Number.isSafeInteger(Number(s)) ? Number(s) : undefined
}

function titleOf(
  entry: { namespace: string; name: string; iid: number; title: string } | undefined,
  namespace: string,
  name: string,
  iid: number,
): string | undefined {
  return entry && entry.namespace === namespace && entry.name === name && entry.iid === iid ? entry.title : undefined
}

// crumbsFor maps a route to its breadcrumbs; every route has at least one.
export function crumbsFor(route: CrumbRoute, names: CrumbNames = {}): Crumb[] {
  const p = route.params
  const namespace = param(p, 'namespace')
  const name = param(p, 'name')
  const overview: Crumb = { label: overviewLabel, to: { name: 'overview', params: {} } }
  const project: Crumb = { label: name, to: { name: 'stream-project', params: { namespace, name } } }
  const milestone = (iid: number): Crumb => ({
    label: milestoneLabel(iid, titleOf(names.milestone, namespace, name, iid)),
    to: { name: 'stream-milestone', params: { namespace, name, iid: String(iid) } },
  })
  const issueCrumb = (iid: number, issue: number): Crumb => ({
    label: issueLabel(issue, titleOf(names.issue, namespace, name, issue)),
    to: { name: 'issue', params: { namespace, name, iid: String(iid), issue: String(issue) } },
  })
  const research: Crumb = { label: researchLabel, to: { name: 'researches', params: {} } }
  const study: Crumb = { label: name, to: { name: 'research', params: { namespace, name } } }
  const runs: Crumb = { label: 'Runs', to: { name: 'research-runs', params: { namespace, name } } }

  let trail: Crumb[]
  switch (route.name) {
    case 'overview':
      trail = [overview]
      break
    case 'inbox':
      trail = [{ label: 'Inbox' }]
      break
    case 'costs':
      trail = [{ label: 'Costs' }]
      break
    case 'stream-project':
      trail = [overview, project]
      break
    case 'stream-milestone': {
      const iid = iidOf(param(p, 'iid'))
      trail = iid ? [overview, project, milestone(iid)] : [overview, project, { label: param(p, 'iid') || 'Milestone' }]
      break
    }
    case 'issue': {
      const iid = iidOf(param(p, 'iid'))
      const issue = iidOf(param(p, 'issue'))
      trail =
        iid && issue
          ? [overview, project, milestone(iid), issueCrumb(iid, issue)]
          : [overview, project, { label: param(p, 'issue') || 'Issue' }]
      break
    }
    case 'task': {
      const task = param(p, 'task')
      const t = names.task
      const known = t && t.namespace === namespace && t.name === name && t.task === task ? t : undefined
      // A task with a milestone and an issue sits under its issue page.
      trail =
        known?.milestone && known.issue
          ? [overview, project, milestone(known.milestone), issueCrumb(known.milestone, known.issue), { label: task }]
          : [overview, project, { label: task || 'Task' }]
      break
    }
    case 'researches':
      trail = [research]
      break
    case 'research-new':
      trail = [research, { label: 'New research' }]
      break
    case 'research':
      trail = [research, study]
      break
    case 'research-runs':
      trail = [research, study, runs]
      break
    case 'research-run': {
      const id = param(p, 'id')
      const r = names.run
      const title = r && r.namespace === namespace && r.name === name && r.id === id ? r.title : undefined
      trail = [research, study, runs, { label: title || id.slice(0, 8) || 'Run' }]
      break
    }
    case 'not-found':
      trail = [{ label: 'Not found' }]
      break
  }
  // The last crumb is the current view: no link.
  const last = trail[trail.length - 1]!
  return [...trail.slice(0, -1), { label: last.label }]
}
