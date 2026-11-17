// Plan milestone in the plan lists (decision 14). Kept free of Vue and the
// client so node --test can import it; PlanList.vue creates the plan and
// opens it.
import type { PlanProject } from './generated/views'

// planStartProjects are the projects the caller may start a plan in (steer),
// sorted by namespace and name. Empty: no button. One: no picker.
export function planStartProjects(projects: readonly PlanProject[]): PlanProject[] {
  return projects
    .filter((p) => p.steer)
    .sort((a, b) => a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name))
}
