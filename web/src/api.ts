import { reactive } from 'vue'
import { ApiError, get } from './client'
import type { Feature, Me, Spend } from './types'

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

// The transport and its errors live in client.ts (R14); re-exported here
// until every call site has moved there.
export { ApiError, del, get, LoginRequiredError, NotFoundError, post } from './client'

export function loginURL(): string {
  const back = window.location.pathname + window.location.search
  return `/oauth2/login?return=${encodeURIComponent(back)}`
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
