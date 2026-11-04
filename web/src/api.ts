import { reactive } from 'vue'
import type { ChatKind, ChatPage, Feature, Me, Spend } from './types'

// Session state shared by the whole app.
export const session = reactive<{
  me: Me | null
  // features are the sections the user may see, null until loaded.
  features: Feature[] | null
  // loginRequired is set when the API answers 401.
  loginRequired: boolean
}>({ me: null, features: null, loginRequired: false })

let featuresSet: () => void
// featuresLoaded resolves once the features are set; route guards wait for it.
export const featuresLoaded = new Promise<void>((resolve) => (featuresSet = resolve))

export function setFeatures(f: Feature[]): void {
  session.features = f
  featuresSet()
}

export function hasFeature(name: Feature['name']): boolean {
  return session.features?.some((f) => f.name === name) ?? false
}

// canStartResearch: the user may start one-off researches.
export function canStartResearch(): boolean {
  return session.features?.some((f) => f.name === 'research' && f.write) ?? false
}

// isResearchPath: the page belongs to the Research section.
export function isResearchPath(path: string): boolean {
  return /^\/research(\/|$)/.test(path)
}

export class NotFoundError extends Error {}

export class LoginRequiredError extends Error {}

export async function get<T>(path: string): Promise<T> {
  const resp = await fetch(`/api/v1${path}`, {
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (resp.status === 401) {
    session.loginRequired = true
    throw new LoginRequiredError('login required')
  }
  if (resp.status === 404) {
    throw new NotFoundError('not found')
  }
  if (!resp.ok) {
    throw new Error(`The server answered ${resp.status}.`)
  }
  return (await resp.json()) as T
}

// ApiError carries the server's error message.
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    // field: the request field the error is about, if the server names one.
    public field?: string,
  ) {
    super(message)
  }
}

export async function post<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(path.startsWith('/oauth2/') ? path : `/api/v1${path}`, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify(body),
  })
  if (resp.status === 401) {
    session.loginRequired = true
    throw new LoginRequiredError('login required')
  }
  const data = (await resp.json().catch(() => ({}))) as { error?: string; field?: string }
  if (!resp.ok) throw new ApiError(data.error ?? `The server answered ${resp.status}.`, resp.status, data.field)
  return data as T
}

export async function del(path: string): Promise<void> {
  const resp = await fetch(`/api/v1${path}`, {
    method: 'DELETE',
    headers: { Accept: 'application/json' },
    credentials: 'same-origin',
  })
  if (resp.status === 401) {
    session.loginRequired = true
    throw new LoginRequiredError('login required')
  }
  if (!resp.ok) {
    const data = (await resp.json().catch(() => ({}))) as { error?: string }
    throw new ApiError(data.error ?? `The server answered ${resp.status}.`, resp.status)
  }
}

export function loginURL(): string {
  const back = window.location.pathname + window.location.search
  return `/oauth2/login?return=${encodeURIComponent(back)}`
}

export async function logout(): Promise<void> {
  const resp = await fetch('/oauth2/logout', { method: 'POST', credentials: 'same-origin' })
  let next = '/'
  if (resp.ok) {
    const body = (await resp.json()) as { redirect?: string }
    next = body.redirect ?? '/'
  }
  window.location.assign(next)
}

function taskChatPath(namespace: string, name: string, task: string): string {
  return `/projects/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/tasks/${encodeURIComponent(task)}/chat`
}

// getTaskChat loads the task's Desk chat (newest first).
export async function getTaskChat(
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
  return get<ChatPage>(`${taskChatPath(namespace, name, task)}${q ? `?${q}` : ''}`)
}

// postTaskChat posts a Desk chat message (retry-safe with clientKey).
export async function postTaskChat(
  namespace: string,
  name: string,
  task: string,
  body: { body: string; clientKey: string; kind?: ChatKind },
): Promise<{ id: number }> {
  return post(taskChatPath(namespace, name, task), body)
}

// getSpend loads what a project, milestone, issue, task or run (task + run)
// cost, with the children of the next level.
export async function getSpend(
  namespace: string,
  project: string,
  scope: { milestone?: number; issue?: number; task?: string; run?: number } = {},
): Promise<Spend> {
  const params = new URLSearchParams({ namespace, project })
  if (scope.milestone) params.set('milestone', String(scope.milestone))
  if (scope.issue) params.set('issue', String(scope.issue))
  if (scope.task) params.set('task', scope.task)
  if (scope.run) params.set('run', String(scope.run))
  return get<Spend>(`/spend?${params}`)
}

// chatPostError turns a chat POST status into plain words for the composer.
export function chatPostError(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.status) {
      case 403:
        return 'You may not chat on this project.'
      case 404:
        return 'Desk chat is not available.'
      case 413:
        return 'The message is larger than 16 KiB.'
      case 429:
        return 'Too many messages; try again in a minute.'
      case 503:
        return 'The chat database is not connected yet.'
      default:
        return e.message
    }
  }
  return e instanceof Error ? e.message : String(e)
}
