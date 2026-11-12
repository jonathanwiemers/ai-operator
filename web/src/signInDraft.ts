// Unsent composer text survives the automatic sign-in (signInAgain.ts): the
// hook keeps it in sessionStorage per route and composer, the composer takes
// it back once after the login returns to the same route.
import { getCurrentScope, onScopeDispose, toValue, type MaybeRefOrGetter, type Ref } from 'vue'
import { onBeforeSignIn } from './signInAgain.ts'

// The route the login returns to (loginURL).
const route = () => window.location.pathname + window.location.search

export function signInDraftKey(path: string, name: string): string {
  return `desk-sign-in-draft:${path}:${name}`
}

// keepDraft stores non-blank text; takeDraft reads and removes it.
export function keepDraft(key: string, text: string): void {
  try {
    if (text.trim()) sessionStorage.setItem(key, text)
  } catch {
    /* private mode / quota */
  }
}

export function takeDraft(key: string): string | null {
  try {
    const text = sessionStorage.getItem(key)
    sessionStorage.removeItem(key)
    return text
  } catch {
    return null // private mode
  }
}

// keepThroughSignIn restores the kept text into an empty composer and keeps
// it before the next automatic sign-in, until the component goes away.
export function keepThroughSignIn(name: MaybeRefOrGetter<string>, text: Ref<string>): void {
  const kept = takeDraft(signInDraftKey(route(), toValue(name)))
  if (kept && !text.value) text.value = kept
  const off = onBeforeSignIn(() => keepDraft(signInDraftKey(route(), toValue(name)), text.value))
  if (getCurrentScope()) onScopeDispose(off)
}
