// plansPath is GET /api/v1/plans, narrowed to one project when given.
// Re-exported by planRows.ts; imported by client.ts on its own to keep the
// initial chunk small.
export function plansPath(project?: { namespace: string; name: string }): string {
  return project ? `/api/v1/plans?project=${encodeURIComponent(`${project.namespace}/${project.name}`)}` : '/api/v1/plans'
}
