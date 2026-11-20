import type { NavigationMenuItem } from '@nuxt/ui'
import type { ProjectSetSummary, ProjectSummary, ResearchSummary } from './generated/views'
import { cronRaw, cronText } from './cron.ts'
import { groupProjects, setCountsLine, setProblem } from './projectSets.ts'
import { milestoneCounts, projectDot, projectNeedsYou, projectStateLine } from './stream.ts'

// The rail's UNavigationMenu items (RailNav.vue, docs/contributing/web-ui.md
// "Navigation"), built here so node can test them: groups of items, the
// pages first, then Projects and Research, each opened by a label item.

export interface RailInput {
  // The route's path: every link's active state comes from it, so a project
  // lights up on its milestone, issue, task and plan pages too.
  path: string
  inboxCount: number
  projects: ProjectSummary[] | null
  // null when not loaded or the request failed: the flat list.
  projectSets?: ProjectSetSummary[] | null
  researches: ResearchSummary[] | null
  features: { history: boolean; plans: boolean; research: boolean }
  // The sidebar is collapsed: the pages only, as icons, the counts as chips.
  collapsed?: boolean
  // Called when a link is selected (the phone sheet closes on it).
  onSelect?: () => void
}

// RailItem: a menu item plus the fields RailNav's slots render. description
// is the sub-line under the label, hint its tooltip; a set (slot 'set', so
// the menu's #set-label slot is typed) carries its discovery time and
// problem for its header line.
interface RailBase extends NavigationMenuItem {
  description?: string
  hint?: string
  children?: RailItem[]
}
export interface RailLink extends RailBase {
  slot?: undefined
}
export interface RailSet extends RailBase {
  slot: 'set'
  discoveredAt?: string
  problem: string | null
}
export type RailItem = RailLink | RailSet

// under: path is the page to or a page below it.
const under = (path: string, to: string) => path === to || path.startsWith(to + '/')
const seg = encodeURIComponent

// The count in the danger tone, never the primary colour; none at 0.
const countBadge = (n: number) => (n > 0 ? { label: String(n), color: 'error' as const, variant: 'soft' as const } : undefined)

const dotColor = { running: 'info', 'needs-you': 'warning', idle: 'success' } as const

// link: a link item with its active state and onSelect. Collapsed, the
// label is hidden, so the link takes it as its accessible name.
function link(input: RailInput, item: RailLink & { to: string }): RailLink {
  const out: RailLink = { ...item, active: item.active ?? under(input.path, item.to), onSelect: input.onSelect }
  if (input.collapsed) out['aria-label'] = item['aria-label'] ?? item.label
  return out
}

function pages(input: RailInput): RailItem[] {
  const { features, inboxCount, collapsed } = input
  const items: RailItem[] = [
    link(input, {
      label: 'Inbox',
      icon: 'i-lucide-inbox',
      to: '/inbox',
      badge: countBadge(inboxCount),
      // Collapsed, the badge is hidden: the count sits on the icon.
      chip: collapsed && inboxCount > 0 ? { text: inboxCount, color: 'error', size: '3xl' } : undefined,
      'aria-label': collapsed && inboxCount > 0 ? `Inbox (${inboxCount})` : undefined,
    }),
    link(input, { label: 'Overview', icon: 'i-lucide-layout-dashboard', to: '/', active: input.path === '/' }),
  ]
  // Metrics read the history database: the feature history says it is there.
  if (features.history) items.push(link(input, { label: 'Metrics', icon: 'i-lucide-receipt', to: '/metrics', 'data-rail-metrics': '' }))
  if (features.plans) items.push(link(input, { label: 'Plans', icon: 'i-lucide-map', to: '/plans', 'data-rail-plans': '' }))
  return items
}

function projectItem(input: RailInput, p: ProjectSummary): RailItem {
  const settling = milestoneCounts(p.activeMilestones, p.settlingMilestones)
  return link(input, {
    label: p.name,
    icon: 'i-lucide-folder-git-2',
    to: `/projects/${seg(p.namespace)}/${seg(p.name)}`,
    chip: { color: dotColor[projectDot(p.stages)] },
    badge: countBadge(projectNeedsYou(p.stages)),
    description: [projectStateLine(p.stages, p.autopilots), settling].filter(Boolean).join(' · '),
    hint: settling ? 'settling: done, waiting up to 7 days for retrospective follow-ups' : undefined,
  })
}

// The Projects group: explicit projects first, then each set with its
// children (D558-P1). A set is a trigger, not a link (it has no page); it
// opens while the route is inside one of its children (openSets).
function projects(input: RailInput): RailItem[] {
  const g = groupProjects(input.projects ?? [], input.projectSets ?? [])
  const items: RailItem[] = [{ label: 'Projects', type: 'label' }]
  items.push(...g.ungrouped.map((p) => projectItem(input, p)))
  for (const { set, children } of g.sets) {
    const kids = children.map((p) => projectItem(input, p))
    items.push({
      label: set.name,
      icon: 'i-lucide-folder-tree',
      type: 'trigger',
      slot: 'set',
      value: `set:${set.namespace}/${set.name}`,
      defaultOpen: kids.some((k) => k.active),
      description: setCountsLine(set.counts),
      discoveredAt: set.lastDiscoveryTime,
      problem: setProblem(set),
      children: kids,
    })
  }
  if (input.projects && !input.projects.length) items.push({ label: 'No projects', type: 'label', class: 'font-normal normal-case tracking-normal' })
  return items
}

// researchSub is a research's sub-line: running, its schedule in words
// with the raw cron as hint (the raw cron alone for a form cronText does
// not read), or one-off.
function researchSub(r: ResearchSummary): { description: string; hint?: string } {
  if (r.phase === 'Running') return { description: 'running' }
  if (r.schedule) return { description: cronText(r.schedule, r.timeZone) ?? r.schedule, hint: cronRaw(r.schedule, r.timeZone) }
  return { description: r.oneOff ? 'one-off' : '' }
}

function research(input: RailInput): RailItem[] {
  const items: RailItem[] = [{ label: 'Research', type: 'label' }]
  for (const r of input.researches ?? []) {
    items.push(
      link(input, {
        label: r.name,
        icon: 'i-lucide-flask-conical',
        to: `/research/${seg(r.namespace)}/${seg(r.name)}`,
        chip: r.phase === 'Running' ? { color: 'info' } : r.phase === 'Succeeded' ? { color: 'success' } : undefined,
        ...researchSub(r),
      }),
    )
  }
  items.push(link(input, { label: 'All research', icon: 'i-lucide-list', to: '/research', active: input.path === '/research' }))
  return items
}

// railItems: the menu's groups. Collapsed, the pages only: a column of
// identical project icons would not tell the projects apart.
export function railItems(input: RailInput): RailItem[][] {
  const groups = [pages(input)]
  if (input.collapsed) return groups
  groups.push(projects(input))
  if (input.features.research) groups.push(research(input))
  return groups
}

// openSets: the values of the sets that should be open (defaultOpen).
// RailNav adds them to the menu's open list: the items arrive after the menu
// mounts, when Reka's accordion no longer reads defaultOpen.
export function openSets(groups: RailItem[][]): string[] {
  return groups.flat().flatMap((i) => (i.defaultOpen && i.value ? [i.value] : []))
}
