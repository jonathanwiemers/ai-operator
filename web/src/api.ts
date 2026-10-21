import { reactive } from 'vue'
import type { Me } from './types'

// Session state shared by the whole app.
export const session = reactive<{
  me: Me | null
  // loginRequired is set when the API answers 401.
  loginRequired: boolean
}>({ me: null, loginRequired: false })

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
