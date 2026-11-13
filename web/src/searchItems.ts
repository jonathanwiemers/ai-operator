// The ⌘K palette's groups, built from loaded data (no Vue, no fetching).
import type { Feature, MilestoneRow, ProjectSummary, SearchResult } from './generated/views'

export interface PaletteItem {
  label: string
  suffix?: string
  icon: string
  to: { name: string; params?: Record<string, string | number> }
}
export interface PaletteGroup {
  id: string
  label: string
  items: PaletteItem[]
  ignoreFilter?: boolean
}

// searchQuery: the trimmed term when the server may be asked, else null.
export function searchQuery(term: string): string | null {
  const q = term.trim()
  return q.length >= 2 ? q : null
}

export function paletteGroups(a: {
  projects: ProjectSummary[]
  milestones: MilestoneRow[]
  results: SearchResult[]
  features: Feature[]
}): PaletteGroup[] {
  const has = (n: string) => a.features.some((f) => f.name === n)
  const pages: PaletteItem[] = [
    { label: 'Inbox', icon: 'i-lucide-inbox', to: { name: 'inbox' } },
    { label: 'Overview', icon: 'i-lucide-layout-dashboard', to: { name: 'overview' } },
  ]
  if (has('history')) pages.push({ label: 'Costs', icon: 'i-lucide-wallet', to: { name: 'costs' } })
  if (has('research')) pages.push({ label: 'Research', icon: 'i-lucide-flask-conical', to: { name: 'researches' } })
  return [
    {
      id: 'projects',
      label: 'Projects',
      items: a.projects.map((p) => ({
        label: p.name,
        suffix: p.namespace,
        icon: 'i-lucide-folder',
        to: { name: 'stream-project', params: { namespace: p.namespace, name: p.name } },
      })),
    },
    {
      id: 'milestones',
      label: 'Milestones',
      items: a.milestones.map((m) => ({
        label: `%${m.iid} ${m.title}`,
        suffix: m.project,
        icon: 'i-lucide-flag',
        to: { name: 'stream-milestone', params: { namespace: m.namespace, name: m.project, iid: m.iid } },
      })),
    },
    {
      id: 'issues',
      label: 'Issues & MRs',
      ignoreFilter: true,
      items: a.results.map((r) => ({
        label: `#${r.issueIID} ${r.title}`,
        suffix: `${r.mergeRequestIID ? `!${r.mergeRequestIID} ` : ''}${r.project}`,
        icon: r.mergeRequestIID ? 'i-lucide-git-pull-request' : 'i-lucide-circle-dot',
        to: { name: 'task', params: { namespace: r.namespace, name: r.project, task: r.task } },
      })),
    },
    { id: 'pages', label: 'Pages', items: pages },
  ]
}
