import type { ProjectSetSummary, ProjectSummary } from './generated/views'

// The rail's project list with set children under their set (R8,
// docs/contributing/web-ui.md, "Navigation").

interface ProjectSetGroup {
  set: ProjectSetSummary
  children: ProjectSummary[]
}

export interface GroupedProjects {
  sets: ProjectSetGroup[]
  ungrouped: ProjectSummary[]
}

const key = (namespace: string, name: string) => `${namespace}/${name}`
const byName = (a: { name: string }, b: { name: string }) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)

// groupProjects puts each set child under its set (same namespace, project.set
// the set's name). Explicit projects and children of a set that is not in
// sets stay in ungrouped, in the order given. Sets are sorted by name, their
// children too; a set without visible children is kept.
export function groupProjects(projects: ProjectSummary[], sets: ProjectSetSummary[]): GroupedProjects {
  const groups = new Map<string, ProjectSetGroup>()
  for (const set of [...sets].sort(byName)) groups.set(key(set.namespace, set.name), { set, children: [] })
  const ungrouped: ProjectSummary[] = []
  for (const p of projects) {
    const g = p.set ? groups.get(key(p.namespace, p.set)) : undefined
    if (g) g.children.push(p)
    else ungrouped.push(p)
  }
  for (const g of groups.values()) g.children.sort(byName)
  return { sets: [...groups.values()], ungrouped }
}

// setCountsLine: "3 active · 12 dormant · 2 excluded · 14 explicit"; zero
// counts are left out, but active (D558-P2).
export function setCountsLine(counts: Partial<ProjectSetSummary['counts']> | undefined): string {
  const c = counts ?? {}
  const parts = [`${c.active ?? 0} active`]
  for (const k of ['dormant', 'excluded', 'explicit'] as const) {
    if (c[k]) parts.push(`${c[k]} ${k}`)
  }
  return parts.join(' · ')
}

// setProblem: the reason of a False Ready or Discovered condition, null when
// neither is False. The header shows it in the warning tone.
export function setProblem(set: Pick<ProjectSetSummary, 'conditions'>): string | null {
  for (const type of ['Ready', 'Discovered']) {
    const c = set.conditions?.find((x) => x.type === type)
    if (c?.status === 'False') return c.message ? `${c.reason}: ${c.message}` : c.reason || `${type} is False`
  }
  return null
}
