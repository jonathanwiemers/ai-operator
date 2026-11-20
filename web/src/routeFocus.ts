// The route-focus decision (Decision 4 of docs/specs/desk-accessible.spec.md),
// pure so node can test it; src/ui/useRouteFocus.ts acts on it.

// A route location as far as the decision needs it: vue-router's start
// location, before the first navigation, matches no record.
export interface FocusLocation {
  path: string
  matched: readonly unknown[]
}

// movesFocus says whether a navigation moves focus to the new page's h1:
// only when the path changes. The first load keeps the browser's default,
// and a new query or hash on the same path (a tab, a filter, a card's
// anchor) keeps focus where it is.
export function movesFocus(from: FocusLocation, to: FocusLocation): boolean {
  if (from.matched.length === 0) return false
  return from.path !== to.path
}
