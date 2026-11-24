// Starting a plan: New plan in the plan lists and the New plan page's
// picker. Kept free of Vue and the client so node --test can import it;
// PlanNew.vue creates the plan and opens it.
import type { PlanProject } from './generated/views'

// planStartProjects are the projects the caller may start a plan in (steer),
// sorted by namespace and name. Empty: no New plan button.
export function planStartProjects(projects: readonly PlanProject[]): PlanProject[] {
  return projects
    .filter((p) => p.steer)
    .sort((a, b) => a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name))
}

// PlanStartItem is a project the New plan picker offers. value is
// "<namespace>/<name>", the ?project= form.
export interface PlanStartItem {
  value: string
  label: string
  description: string
  namespace: string
  name: string
}

// PlanStartGroup is a group of the picker: Projects, or one project set.
export interface PlanStartGroup {
  label: string
  items: PlanStartItem[]
}

export const dormantDescription = 'dormant, starts with the plan'

const key = (p: { namespace: string; name: string }) => `${p.namespace}/${p.name}`

// planStartGroups are the New plan picker's groups (Decision 5): first
// Projects, the AgentProjects the caller may steer by namespace and name;
// then one group per set by name, its dormant entries the caller may steer
// by path. A dormant entry whose AgentProject exists shows only as that
// project. Empty groups are left out.
export function planStartGroups(projects: readonly PlanProject[]): PlanStartGroup[] {
  const agentProjects = new Set(projects.filter((p) => !p.dormant).map(key))
  const own: PlanStartItem[] = planStartProjects(projects.filter((p) => !p.dormant)).map((p) => ({
    value: key(p),
    label: key(p),
    description: p.path,
    namespace: p.namespace,
    name: p.name,
  }))
  const sets = new Map<string, PlanProject[]>()
  for (const p of projects) {
    if (!p.dormant || !p.steer || agentProjects.has(key(p))) continue
    const set = p.set ?? ''
    sets.set(set, [...(sets.get(set) ?? []), p])
  }
  const groups: PlanStartGroup[] = own.length ? [{ label: 'Projects', items: own }] : []
  for (const set of [...sets.keys()].sort((a, b) => a.localeCompare(b))) {
    const items = sets
      .get(set)!
      .sort((a, b) => a.path.localeCompare(b.path))
      .map((p) => ({ value: key(p), label: p.path, description: dormantDescription, namespace: p.namespace, name: p.name }))
    groups.push({ label: set, items })
  }
  return groups
}

// planStartMenuItems are the groups as USelectMenu items: one array per
// group, led by its label.
// The label item has value only for USelectMenu's value-key typing.
export function planStartMenuItems(
  groups: readonly PlanStartGroup[],
): (PlanStartItem | { type: 'label'; label: string; value?: undefined })[][] {
  return groups.map((g) => [{ type: 'label' as const, label: g.label }, ...g.items])
}

// planNewTo is where New plan in a plan list goes: the New plan page, with
// ?project=<namespace>/<name> on a project's Plans tab to preselect it.
export function planNewTo(project?: { namespace: string; name: string }): { name: 'plan-new'; query?: { project: string } } {
  return project ? { name: 'plan-new', query: { project: key(project) } } : { name: 'plan-new' }
}
