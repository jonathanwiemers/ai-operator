// The Desk's client of the UI API (docs/ui.md, "Client"): one function per
// route, named verb + resource, with path parameters as arguments and
// encoded here. Return types come from generated/views.ts. No get<T> or
// post<T> outside this file.
import { session } from './api'
import type { Feature, FeaturesResponse, InboxResponse, Me, OverviewView, ProjectSummary, ResearchSummary } from './generated/views'

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

// getOverview: GET /overview, the dashboard's Overview tab.
export function getOverview(): Promise<OverviewView> {
  return get<OverviewView>('/overview')
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
