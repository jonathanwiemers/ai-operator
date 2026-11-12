// Signing in again by itself when the API answers 401 (docs/ui.md,
// "Security"): a top-level navigation to the login, once per minute, so a
// login that keeps failing ends on the sign-in page instead of a loop.

const markerKey = 'desk-sign-in-again'
const guardMs = 60_000

// loginURL is the login that comes back to the current route.
export function loginURL(): string {
  const back = window.location.pathname + window.location.search
  return `/oauth2/login?return=${encodeURIComponent(back)}`
}

// shouldSignInAgain: no attempt in the last minute (marker, a timestamp in
// ms) and no failed login in the URL.
export function shouldSignInAgain(marker: string | null, now: number, search: string): boolean {
  if (new URLSearchParams(search).has('login_error')) return false
  const at = Number(marker)
  return marker === null || !Number.isFinite(at) || now - at >= guardMs
}

const hooks = new Set<() => void>()

// onBeforeSignIn registers fn to run before the automatic sign-in (to keep
// drafts); it returns the function that removes it.
export function onBeforeSignIn(fn: () => void): () => void {
  hooks.add(fn)
  return () => hooks.delete(fn)
}

let navigating = false

// signInAgain goes to the login if allowed and says whether it does. Later
// calls on the same page say true too, so the sign-in page never flashes.
export function signInAgain(): boolean {
  if (navigating) return true
  try {
    const storage = window.sessionStorage
    if (!shouldSignInAgain(storage.getItem(markerKey), Date.now(), window.location.search)) return false
    storage.setItem(markerKey, String(Date.now()))
  } catch {
    return false // Without storage there is no loop guard: no automatic sign-in.
  }
  navigating = true
  for (const fn of hooks) {
    try {
      fn()
    } catch {
      // A lost draft must not stop the login.
    }
  }
  window.location.assign(loginURL())
  return true
}
