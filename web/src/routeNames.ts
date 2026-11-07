// The names of the Desk's routes (web/src/main.ts types its route records
// against RouteName, so a route whose name is missing here does not
// compile). Kept free of Vue imports so node --test can iterate it:
// crumbs.test.mjs fails for a route without breadcrumbs.
export const routeNames = [
  'overview',
  'everything',
  'inbox',
  'stream-project',
  'stream-milestone',
  'task',
  'project-board',
  'milestone',
  'researches',
  'research-new',
  'research',
  'research-runs',
  'research-run',
  'not-found',
] as const

export type RouteName = (typeof routeNames)[number]

export function isRouteName(v: unknown): v is RouteName {
  return typeof v === 'string' && (routeNames as readonly string[]).includes(v)
}
