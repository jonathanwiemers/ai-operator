// The Desk's theme: Light, Dark or Auto (follow the system), chosen in the
// account menu at the bottom of the sidebar (components/RailNav.vue,
// accountMenu.ts) and kept per browser in localStorage under desk.theme.
// style.css holds the light tokens on :root
// and the dark ones on .dark; themeController keeps the `dark` class on
// <html> in step with the choice and, in Auto, with prefers-color-scheme.
// main.ts creates it before the app mounts; the CSP allows no inline script.

import type { InjectionKey } from 'vue'

export type Theme = 'light' | 'dark' | 'auto'

export const themes: readonly Theme[] = ['light', 'dark', 'auto']

// The parts of MediaQueryList, DOMTokenList and Storage used here, so tests
// can pass fakes.
export interface SchemeQuery {
  readonly matches: boolean
  addEventListener(type: 'change', listener: (e: { readonly matches: boolean }) => void): void
}

export interface ClassToggle {
  toggle(token: string, force?: boolean): boolean
}

export interface ThemeStorage {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
}

export interface ThemeController {
  get(): Theme
  set(theme: Theme): void
}

export const darkQuery = '(prefers-color-scheme: dark)'
export const themeKey = 'desk.theme'

// The controller main.ts provides to the app, for the account menu (RailNav.vue).
export const themeInjectionKey: InjectionKey<ThemeController> = Symbol('theme')

const isTheme = (v: unknown): v is Theme => themes.includes(v as Theme)

// readTheme is the stored choice; missing, unknown or unreadable storage
// (privacy modes throw) is Auto.
export function readTheme(storage: ThemeStorage | null): Theme {
  try {
    const v = storage?.getItem(themeKey)
    return isTheme(v) ? v : 'auto'
  } catch {
    return 'auto'
  }
}

// themeController applies the stored choice at once and registers one change
// listener on query, which sets `dark` only while the choice is Auto. set
// stores a new choice (a failing write keeps it for this page) and applies it.
export function themeController(classes: ClassToggle, query: SchemeQuery, storage: ThemeStorage | null): ThemeController {
  let theme = readTheme(storage)
  const apply = () => classes.toggle('dark', theme === 'auto' ? query.matches : theme === 'dark')
  apply()
  query.addEventListener('change', (e) => {
    if (theme === 'auto') classes.toggle('dark', e.matches)
  })
  return {
    get: () => theme,
    set(next) {
      theme = next
      try {
        storage?.setItem(themeKey, next)
      } catch {
        // The choice holds until the page reloads.
      }
      apply()
    },
  }
}
