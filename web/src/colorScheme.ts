// The Desk follows the system's colour scheme (no toggle): style.css holds
// the light tokens on :root and the dark ones on .dark, and followColorScheme
// keeps the `dark` class on <html> in step with prefers-color-scheme. It runs
// from main.ts before the app is created; the CSP allows no inline script.

// The parts of MediaQueryList and DOMTokenList used here, so tests can pass
// fakes.
export interface SchemeQuery {
  readonly matches: boolean
  addEventListener(type: 'change', listener: (e: { readonly matches: boolean }) => void): void
}

export interface ClassToggle {
  toggle(token: string, force?: boolean): boolean
}

export const darkQuery = '(prefers-color-scheme: dark)'

// followColorScheme sets `dark` on classes when query matches now and
// whenever the system scheme changes.
export function followColorScheme(classes: ClassToggle, query: SchemeQuery): void {
  classes.toggle('dark', query.matches)
  query.addEventListener('change', (e) => classes.toggle('dark', e.matches))
}
