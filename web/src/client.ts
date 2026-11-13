// The Desk's client of the UI API (docs/ui.md, "Client"): one function per
// route, named verb + resource, with path parameters as arguments and
// encoded here. Return types come from generated/views.ts. No get<T> or
// post<T> outside this file.
import { session } from './api'
import { signInAgain } from './signInAgain'
import type {
  About,
  ChatPage,
  ChatPostResponse,
  CostsTopView,
  CostsView,
  Feature,
  FollowUpMilestoneView,
  FeaturesResponse,
  GitLabStatusView,
  HistoryResponse,
  InboxResponse,
  IssueView,
  Me,
  MilestoneList,
  MilestoneView,
  NoteView,
  OverviewView,
  ProjectDetail,
  ProjectSummary,
  ResearchCreated,
  ResearchForm,
  ResearchResponse,
  ResearchRunsPage,
  ResearchSummary,
  SearchResponse,
  SpendView,
  StreamPage,
  TaskResponse,
  ThreadPart,
  ThreadResponse,
} from './generated/views'
import type { ResearchAgent, ResearchDepth, ResearchView } from './generated/api'
import type { ChatKind } from './types'

export class NotFoundError extends Error {}

export class LoginRequiredError extends Error {}

// ApiError carries the server's error message.
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    // field: the request field the error is about, if the server names one.
    public field?: string,
    // data: the whole error body, for endpoints that add details.
    public data: Record<string, unknown> = {},
  ) {
    super(message)
  }
}

// loginRequired: sign in again by itself once (signInAgain.ts), else show
// the sign-in page.
function loginRequired(): never {
  if (!signInAgain()) session.loginRequired = true
  throw new LoginRequiredError('login required')
}

// get, post and del are the transport, private to this file: a new route
// gets a function below.
async function get<T>(path: string): Promise<T> {
  const resp = await fetch(`/api/v1${path}`, {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (resp.status === 401) loginRequired()
  if (resp.status === 404) {
    throw new NotFoundError('not found')
  }
  if (!resp.ok) {
    throw new ApiError(`The server answered ${resp.status}.`, resp.status)
  }
  return (await resp.json()) as T
}

async function post<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(path.startsWith('/oauth2/') ? path : `/api/v1${path}`, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify(body),
  })
  if (resp.status === 401) loginRequired()
  const data = (await resp.json().catch(() => ({}))) as { error?: string; field?: string }
  if (!resp.ok) throw new ApiError(data.error ?? `The server answered ${resp.status}.`, resp.status, data.field, data)
  return data as T
}

async function del(path: string): Promise<void> {
  const resp = await fetch(`/api/v1${path}`, {
    method: 'DELETE',
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (resp.status === 401) loginRequired()
  if (!resp.ok) {
    const data = (await resp.json().catch(() => ({}))) as { error?: string }
    throw new ApiError(data.error ?? `The server answered ${resp.status}.`, resp.status)
  }
}

// getMe: GET /me, the signed-in user and how users sign in.
export function getMe(): Promise<Me> {
  return get<Me>('/me')
}

// getFeatures: GET /features, unwrapped to the list of sections the user
// may see.
export async function getFeatures(): Promise<Feature[]> {
  return (await get<FeaturesResponse>('/features')).features
}

// getAbout: GET /about, the running version and the newest release.
export function getAbout(): Promise<About> {
  return get<About>('/about')
}

// getProjects: GET /projects, the projects the user may see.
export function getProjects(): Promise<ProjectSummary[]> {
  return get<ProjectSummary[]>('/projects')
}

// getInbox: GET /inbox. The envelope is kept: the views read items and
// guard against a missing list.
export function getInbox(): Promise<InboxResponse> {
  return get<InboxResponse>('/inbox')
}

// getResearches: GET /researches, the researches the user may see.
export function getResearches(): Promise<ResearchSummary[]> {
  return get<ResearchSummary[]>('/researches')
}

// getResearch: GET /researches/{namespace}/{name}, the research with its
// latest run and result.
export function getResearch(namespace: string, name: string): Promise<ResearchResponse> {
  return get<ResearchResponse>(researchPath(namespace, name))
}

// getResearchRun: GET /researches/{namespace}/{name}/runs/{id}, one run of
// the research and its result.
export function getResearchRun(namespace: string, name: string, id: string): Promise<ResearchResponse> {
  return get<ResearchResponse>(`${researchPath(namespace, name)}/runs/${encodeURIComponent(id)}`)
}

function researchPath(namespace: string, name: string): string {
  return `/researches/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`
}

// getResearchRuns: GET /researches/{namespace}/{name}/runs, a page of the
// research's runs, newest first; before is the startedAt of the last run
// shown.
export function getResearchRuns(
  namespace: string,
  name: string,
  opts: { before?: string; limit?: number } = {},
): Promise<ResearchRunsPage> {
  const q = new URLSearchParams()
  if (opts.limit) q.set('limit', String(opts.limit))
  if (opts.before) q.set('before', opts.before)
  const qs = q.toString()
  return get<ResearchRunsPage>(`${researchPath(namespace, name)}/runs${qs ? `?${qs}` : ''}`)
}

// ResearchCreate is the body of POST /researches (Go: createResearchRequest,
// not a view); without agent the project's default runs it.
export interface ResearchCreate {
  project: string
  prompt: string
  depth: ResearchDepth
  view: ResearchView
  topics: string[]
  agent?: ResearchAgent
}

// createResearch: POST /researches, a one-off research. A 400 names the
// field it is about in ApiError.field.
export function createResearch(body: ResearchCreate): Promise<ResearchCreated> {
  return post<ResearchCreated>('/researches', body)
}

// deleteResearch: DELETE /researches/{namespace}/{name}.
export function deleteResearch(namespace: string, name: string): Promise<void> {
  return del(researchPath(namespace, name))
}

// getResearchForm: GET /research/form, what the new research form offers.
export function getResearchForm(): Promise<ResearchForm> {
  return get<ResearchForm>('/research/form')
}

// getOverview: GET /overview, the dashboard's Overview tab.
export function getOverview(): Promise<OverviewView> {
  return get<OverviewView>('/overview')
}

function projectPath(namespace: string, name: string): string {
  return `/projects/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`
}

function milestonePath(namespace: string, name: string, iid: number | string): string {
  return `${projectPath(namespace, name)}/milestones/${encodeURIComponent(iid)}`
}

function taskPath(namespace: string, name: string, task: string): string {
  return `${projectPath(namespace, name)}/tasks/${encodeURIComponent(task)}`
}

// getProject: GET /projects/{namespace}/{name}, the project with its tasks
// grouped by milestone.
export function getProject(namespace: string, name: string): Promise<ProjectDetail> {
  return get<ProjectDetail>(projectPath(namespace, name))
}

// getProjectHistory: GET /projects/{namespace}/{name}/history, the
// project's activity per day over the last days, in the time zone tz.
export function getProjectHistory(namespace: string, name: string, days: number, tz: string): Promise<HistoryResponse> {
  const q = new URLSearchParams({ days: String(days), tz })
  return get<HistoryResponse>(`${projectPath(namespace, name)}/history?${q}`)
}

// getStream: GET /stream, a page of events. The query (project, milestone,
// issue, kinds, q, before, after, limit) is the caller's.
export function getStream(query: URLSearchParams): Promise<StreamPage> {
  return get<StreamPage>(`/stream?${query}`)
}

// getMilestones: GET /milestones, the open or done milestones, across
// projects or of one ("namespace/name").
export function getMilestones(opts: { state: 'open' | 'done' | 'all'; limit?: number; project?: string }): Promise<MilestoneList> {
  const q = new URLSearchParams({ state: opts.state })
  if (opts.limit) q.set('limit', String(opts.limit))
  if (opts.project) q.set('project', opts.project)
  return get<MilestoneList>(`/milestones?${q}`)
}

// getSearch: GET /search?q=, the issues and merge requests matching q.
export function getSearch(q: string): Promise<SearchResponse> {
  return get<SearchResponse>(`/search?${new URLSearchParams({ q })}`)
}

// getMilestone: GET /projects/{namespace}/{name}/milestones/{iid}.
export function getMilestone(namespace: string, name: string, iid: number | string): Promise<MilestoneView> {
  return get<MilestoneView>(milestonePath(namespace, name, iid))
}

// getMilestoneIssue: GET .../milestones/{iid}/issues/{issue}, the issue's
// row with the name of its live task.
export function getMilestoneIssue(
  namespace: string,
  name: string,
  iid: number | string,
  issue: number | string,
): Promise<IssueView> {
  return get<IssueView>(`${milestonePath(namespace, name, iid)}/issues/${encodeURIComponent(issue)}`)
}

// getMilestoneThread: GET .../milestones/{iid}/thread, the tracking issue's
// comments, read with the user's GitLab account.
export function getMilestoneThread(namespace: string, name: string, iid: number): Promise<ThreadResponse> {
  return get<ThreadResponse>(`${milestonePath(namespace, name, iid)}/thread`)
}

// postMilestoneNote: POST .../milestones/{iid}/notes, a message to the
// milestone's pilot on its tracking issue, in the user's GitLab name (the
// server adds the pilot prefix).
export function postMilestoneNote(namespace: string, name: string, iid: number, body: string): Promise<NoteView> {
  return post<NoteView>(`${milestonePath(namespace, name, iid)}/notes`, { body })
}

// createFollowUp: POST .../milestones/{iid}/followup, a milestone made of
// the retrospective's follow-ups (by number). A 409 lists the ones already
// taken in ApiError.data.taken.
export function createFollowUp(
  namespace: string,
  name: string,
  iid: number,
  body: { followUps: number[]; title: string; description: string },
): Promise<FollowUpMilestoneView> {
  return post<FollowUpMilestoneView>(`${milestonePath(namespace, name, iid)}/followup`, body)
}

// dismissFollowUps: POST .../milestones/{iid}/followups/dismiss, the
// owner's "create none" on the retrospective's thread.
export async function dismissFollowUps(namespace: string, name: string, iid: number): Promise<void> {
  await post(`${milestonePath(namespace, name, iid)}/followups/dismiss`, {})
}

// getSpend: GET /spend, what a project, milestone, issue, task or run (task
// + run) cost, with the children of the next level.
export function getSpend(
  namespace: string,
  project: string,
  scope: { milestone?: number; issue?: number; task?: string; run?: number } = {},
): Promise<SpendView> {
  const params = new URLSearchParams({ namespace, project })
  if (scope.milestone) params.set('milestone', String(scope.milestone))
  if (scope.issue) params.set('issue', String(scope.issue))
  if (scope.task) params.set('task', scope.task)
  if (scope.run) params.set('run', String(scope.run))
  return get<SpendView>(`/spend?${params}`)
}

// CostsParams is the query of GET /costs (Go: parseCostParams, not a view):
// a named range or from and to (YYYY-MM-DD, inclusive, both), the grouping,
// the filters (project as "namespace/name") and the viewer's IANA time zone.
// Absent ones take the server's defaults (30d, project, UTC).
export interface CostsParams {
  range?: Exclude<CostsView['range'], 'custom'>
  from?: string
  to?: string
  group?: CostsView['group']
  project?: string
  kind?: string
  engine?: string
  tz?: string
}

// getCosts: GET /costs, what the projects the user may see cost over a
// range: series by the group, totals, breakdowns and the month's days.
export function getCosts(params: CostsParams = {}): Promise<CostsView> {
  return get<CostsView>(`/costs${costsQuery(params)}`)
}

// getCostsTop: GET /costs/top, the most expensive milestones, issues and
// runs of the same range and filters (group is ignored by the server).
export function getCostsTop(params: CostsParams = {}): Promise<CostsTopView> {
  return get<CostsTopView>(`/costs/top${costsQuery(params)}`)
}

function costsQuery(params: CostsParams): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v) q.set(k, v)
  }
  const qs = q.toString()
  return qs ? `?${qs}` : ''
}

// getTask: GET /projects/{namespace}/{name}/tasks/{task}.
export function getTask(namespace: string, name: string, task: string): Promise<TaskResponse> {
  return get<TaskResponse>(taskPath(namespace, name, task))
}

// getTaskThread: GET .../tasks/{task}/thread, the comments of the task's
// issue, merge request and tracking issue, read with the user's GitLab
// account.
export function getTaskThread(namespace: string, name: string, task: string): Promise<ThreadResponse> {
  return get<ThreadResponse>(`${taskPath(namespace, name, task)}/thread`)
}

// postTaskNote: POST .../tasks/{task}/notes, a comment on one of the task's
// threads (steering), in the user's GitLab name.
export function postTaskNote(
  namespace: string,
  name: string,
  task: string,
  target: ThreadPart['target'],
  body: string,
): Promise<NoteView> {
  return post<NoteView>(`${taskPath(namespace, name, task)}/notes`, { target, body })
}

// getTaskChat: GET .../tasks/{task}/chat, the task's Desk chat (newest
// first).
export function getTaskChat(
  namespace: string,
  name: string,
  task: string,
  opts: { before?: number; after?: number; limit?: number } = {},
): Promise<ChatPage> {
  const params = new URLSearchParams()
  if (opts.before) params.set('before', String(opts.before))
  if (opts.after) params.set('after', String(opts.after))
  if (opts.limit) params.set('limit', String(opts.limit))
  const q = params.toString()
  return get<ChatPage>(`${taskPath(namespace, name, task)}/chat${q ? `?${q}` : ''}`)
}

// postTaskChat: POST .../tasks/{task}/chat, a Desk chat message, interrupt
// or command (retry-safe with clientKey).
export function postTaskChat(
  namespace: string,
  name: string,
  task: string,
  body: { body: string; clientKey: string; kind?: ChatKind },
): Promise<ChatPostResponse> {
  return post<ChatPostResponse>(`${taskPath(namespace, name, task)}/chat`, body)
}

// transcriptURL: GET .../tasks/{task}/transcript, the task pod's live
// transcript as server-sent events. An EventSource is not a fetch, so the
// client gives its URL.
export function transcriptURL(namespace: string, name: string, task: string): string {
  return `/api/v1${taskPath(namespace, name, task)}/transcript`
}

// eventsURL: GET /events, the change events as server-sent events, for an
// EventSource.
export function eventsURL(): string {
  return '/api/v1/events'
}

// getGitLabStatus: GET /gitlab, whether the user's GitLab account is
// connected, and where to connect it.
export function getGitLabStatus(): Promise<GitLabStatusView> {
  return get<GitLabStatusView>('/gitlab')
}

// disconnectGitLab: POST /oauth2/gitlab/logout, forgets the user's GitLab
// token.
export async function disconnectGitLab(): Promise<void> {
  await post('/oauth2/gitlab/logout', {})
}

// logout: POST /oauth2/logout, then go where the server says (the
// provider's logout page, or the Desk).
export async function logout(): Promise<void> {
  const resp = await fetch('/oauth2/logout', { method: 'POST', credentials: 'same-origin' })
  let next = '/'
  if (resp.ok) {
    const body = (await resp.json()) as { redirect?: string }
    next = body.redirect ?? '/'
  }
  window.location.assign(next)
}
