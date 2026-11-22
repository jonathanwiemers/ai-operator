// plansPath is GET /api/v1/plans, narrowed to one project when given.
// Own module, so client.ts (initial chunk) does not pull in planRows.ts.
export function plansPath(project?: { namespace: string; name: string }): string {
  return project ? `/api/v1/plans?project=${encodeURIComponent(`${project.namespace}/${project.name}`)}` : '/api/v1/plans'
}
