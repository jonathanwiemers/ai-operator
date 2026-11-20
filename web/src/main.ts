import { createApp } from 'vue'
import uiIcons from '@nuxt/ui/runtime/vue/plugins/icons.js'
import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import App from './App.vue'
import { canStartResearch, featuresLoaded, hasFeature, isResearchPath } from './api'
import { darkQuery, themeController, themeInjectionKey, type ThemeStorage } from './colorScheme'
import { costsRedirect } from './costsRedirect'
import type { RouteName } from './routeNames'
import { listenForSheets } from './ui/useBackClose'
import './style.css'

// Reading window.localStorage itself throws where storage is blocked; the
// theme then is Auto and is not kept.
function localStore(): ThemeStorage | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

// Before the app mounts, so the first paint has the chosen theme.
const theme = themeController(document.documentElement.classList, window.matchMedia(darkQuery), localStore())

// Views load with their route, each as its own chunk. Only App.vue (the shell,
// with SignIn.vue) is in the initial bundle, which scripts/bundle-budget.mjs
// keeps within bundle-budget.json (docs/contributing/web-ui.md "Bundle budget").
const Dashboard = () => import('./views/Dashboard.vue')
const Stream = () => import('./views/Stream.vue')
const Inbox = () => import('./views/Inbox.vue')
const Metrics = () => import('./views/Metrics.vue')
const TaskView = () => import('./views/Task.vue')
const IssueView = () => import('./views/Issue.vue')
const Researches = () => import('./views/Researches.vue')
const ResearchNew = () => import('./views/ResearchNew.vue')
const ResearchView = () => import('./views/Research.vue')
const ResearchRunsView = () => import('./views/ResearchRuns.vue')
const ResearchRunView = () => import('./views/ResearchRun.vue')
const AllPlans = () => import('./views/AllPlans.vue')
const PlanView = () => import('./views/Plan.vue')
const NotFound = () => import('./views/NotFound.vue')

// Every route is named, with a name from routeNames: crumbsFor and its test
// cover each one.
// The old /costs URL keeps its query on the way to /metrics; it has no name of its own.
const redirects: RouteRecordRaw[] = [{ path: '/costs', redirect: costsRedirect }]

const routes: (RouteRecordRaw & { name: RouteName })[] = [
  { path: '/', name: 'overview', component: Dashboard },
  { path: '/inbox', name: 'inbox', component: Inbox },
  { path: '/metrics', name: 'metrics', component: Metrics },
  { path: '/plans', name: 'all-plans', component: AllPlans },
  { path: '/projects/:namespace/:name', name: 'stream-project', component: Stream, props: true },
  {
    path: '/projects/:namespace/:name/milestones/:iid',
    name: 'stream-milestone',
    component: Stream,
    props: true,
  },
  // One problem's thread on the Needs you tab (spec desk-problem-thread,
  // Decision 11); :problem is its row id or issue-<iid>.
  {
    path: '/projects/:namespace/:name/milestones/:iid/problems/:problem',
    name: 'problem',
    component: Stream,
    props: true,
  },
  {
    path: '/projects/:namespace/:name/milestones/:iid/issues/:issue',
    name: 'issue',
    component: IssueView,
    props: true,
  },
  { path: '/projects/:namespace/:name/tasks/:task', name: 'task', component: TaskView, props: true },
  // One plan; the project's plan list is its Plans tab, where old list links
  // go (R14).
  { path: '/projects/:namespace/:name/plans/:plan', name: 'plans', component: PlanView, props: true },
  {
    path: '/projects/:namespace/:name/plans',
    name: 'plans-list',
    redirect: (to) => ({ name: 'stream-project', params: to.params, query: { ...to.query, tab: 'plans' } }),
  },
  { path: '/research', name: 'researches', component: Researches },
  { path: '/research/new', name: 'research-new', component: ResearchNew },
  { path: '/research/:namespace/:name', name: 'research', component: ResearchView, props: true },
  { path: '/research/:namespace/:name/runs', name: 'research-runs', component: ResearchRunsView, props: true },
  { path: '/research/:namespace/:name/runs/:id', name: 'research-run', component: ResearchRunView, props: true },
  { path: '/:rest(.*)*', name: 'not-found', component: NotFound },
]

// The sheets' popstate listener comes before vue-router's, so a back that
// closes a sheet does not reach the router (src/ui/useBackClose.ts).
listenForSheets()
const router = createRouter({
  history: createWebHistory(),
  routes: [...redirects, ...routes],
  // A prompt link scrolls to its card: the card scrolls itself into view
  // when it mounts, which the jump to the top would undo on an in-app
  // navigation (the window scrolls on phones). A missing card leaves the
  // scroll to the card once it loads. A new hash on the same page (a
  // decision card's link to its successor) does not scroll: the card the
  // hash names scrolls itself into view (MilestoneDecisions.vue).
  scrollBehavior: (to, from, saved) => {
    if (saved) return saved
    if (to.hash.startsWith('#prompt-')) return { el: to.hash, top: 12 }
    if (to.hash && to.hash !== from.hash && to.fullPath.split('#')[0] === from.fullPath.split('#')[0]) return false
    return { top: 0 }
  },
})

// Research pages exist only with the research feature; without it they are
// not found, under their own URL, and so is the one-off form without the
// write grant. Users with research alone start there.
router.beforeEach(async (to) => {
  const research = isResearchPath(to.path)
  const plans = to.name === 'plans' || to.name === 'all-plans'
  if (!research && !plans && to.path !== '/') return true
  await featuresLoaded
  if (
    (research && !hasFeature('research')) ||
    (to.name === 'research-new' && !canStartResearch()) ||
    (plans && !hasFeature('plans'))
  ) {
    return { name: 'not-found', params: { rest: to.path.slice(1).split('/') }, query: to.query, hash: to.hash }
  }
  if (to.path === '/' && !hasFeature('factory') && hasFeature('research')) return '/research'
  return true
})

// Of Nuxt UI's Vue plugin (@nuxt/ui/vue-plugin) only the icons: the whole
// plugin would add the colour plugin, whose <style> the CSP refuses
// (style.css holds the colours instead), and an unhead instance nothing here
// uses (docs/contributing/web-ui.md, "UI kit"). Links need no plugin, they use vue-router.
createApp(App).use(router).use(uiIcons).provide(themeInjectionKey, theme).mount('#app')
