// The Desk's client of the UI API (docs/ui.md, "Client"): one function per
// route, named verb + resource, with path parameters as arguments and
// encoded here. Return types come from generated/views.ts. No get<T> or
// post<T> outside this file.
import { session } from './api'
import type {
  ChatPage,
  ChatPostResponse,
  Feature,
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
  ResearchForm,
  ResearchResponse,
  ResearchSummary,
  StreamPage,
  TaskResponse,
  ThreadPart,
  ThreadResponse,
} from './generated/views'
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

function loginRequired(): never {
  session.loginRequired = true
  throw new LoginRequiredError('login required')
}

// get, post and del are the transport. They are exported only for api.ts,
// which re-exports them to the call sites not moved yet (R14); new code
// adds a route function below instead.
export async function get<T>(path: string): Promise<T> {
  const resp = await fetch(`/api/v1${path}`, {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (resp.status === 401) loginRequired()
  if (resp.status === 404) {
    throw new NotFoundError('not found')
  }
  if (!resp.ok) {
    throw new Error(`The server answered ${resp.status}.`)
  }
  return (await resp.json()) as T
}

export async function post<T>(path: string, body: unknown): Promise<T> {
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

export async function del(path: string): Promise<void> {
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
  return get<ResearchResponse>(`/researches/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`)
}

// getResearchRun: GET /researches/{namespace}/{name}/runs/{id}, one run of
// the research and its result.
export function getResearchRun(namespace: string, name: string, id: string): Promise<ResearchResponse> {
  return get<ResearchResponse>(
    `/researches/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/runs/${encodeURIComponent(id)}`,
  )
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
export function getMilestones(opts: { state: 'open' | 'done'; limit?: number; project?: string }): Promise<MilestoneList> {
  const q = new URLSearchParams({ state: opts.state })
  if (opts.limit) q.set('limit', String(opts.limit))
  if (opts.project) q.set('project', opts.project)
  return get<MilestoneList>(`/milestones?${q}`)
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
