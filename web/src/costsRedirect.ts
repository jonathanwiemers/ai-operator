// The old /costs URL moved to /metrics (Desk: one Metrics page): the query
// and the hash are kept, so old links keep their filters. Pure so that
// node --test can cover it (test/costsRedirect.test.mjs).
type Query = Record<string, string | null | (string | null)[]>

export function costsRedirect(to: { query: Query; hash: string }): { path: string; query: Query; hash: string } {
  return { path: '/metrics', query: to.query, hash: to.hash }
}
