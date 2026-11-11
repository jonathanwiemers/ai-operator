import { createApp } from 'vue'
import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import App from './App.vue'
import { canStartResearch, featuresLoaded, hasFeature, isResearchPath } from './api'
import type { RouteName } from './routeNames'
import './style.css'

// Views load with their route, each as its own chunk. Only App.vue (the shell,
// with SignIn.vue) is in the initial bundle, which scripts/bundle-budget.mjs
// keeps within bundle-budget.json (docs/ui.md "Bundle budget").
const Dashboard = () => import('./views/Dashboard.vue')
const Stream = () => import('./views/Stream.vue')
const Inbox = () => import('./views/Inbox.vue')
const TaskView = () => import('./views/Task.vue')
const IssueView = () => import('./views/Issue.vue')
const Researches = () => import('./views/Researches.vue')
const ResearchNew = () => import('./views/ResearchNew.vue')
const ResearchView = () => import('./views/Research.vue')
const ResearchRunsView = () => import('./views/ResearchRuns.vue')
const ResearchRunView = () => import('./views/ResearchRun.vue')
const NotFound = () => import('./views/NotFound.vue')

// Every route is named, with a name from routeNames: crumbsFor and its test
// cover each one.
const routes: (RouteRecordRaw & { name: RouteName })[] = [
  { path: '/', name: 'overview', component: Dashboard },
  { path: '/inbox', name: 'inbox', component: Inbox },
  { path: '/projects/:namespace/:name', name: 'stream-project', component: Stream, props: true },
  {
    path: '/projects/:namespace/:name/milestones/:iid',
    name: 'stream-milestone',
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
  // Old pages, kept as redirects (with their names) so links and bookmarks work.
  // The Everything stream is the dashboard's Stream tab.
  {
    path: '/everything',
    name: 'everything',
    redirect: (to) => ({ name: 'overview', query: { ...to.query, tab: 'stream' }, hash: to.hash }),
  },
  {
    path: '/projects/:namespace/:name/board',
    name: 'project-board',
    redirect: (to) => ({ name: 'stream-project', params: to.params, query: { tab: 'board' } }),
  },
  {
    path: '/projects/:namespace/:name/milestones/:iid/detail',
    name: 'milestone',
    redirect: (to) => ({ name: 'stream-milestone', params: to.params, query: { tab: 'stream' } }),
  },
  { path: '/research', name: 'researches', component: Researches },
  { path: '/research/new', name: 'research-new', component: ResearchNew },
  { path: '/research/:namespace/:name', name: 'research', component: ResearchView, props: true },
  { path: '/research/:namespace/:name/runs', name: 'research-runs', component: ResearchRunsView, props: true },
  { path: '/research/:namespace/:name/runs/:id', name: 'research-run', component: ResearchRunView, props: true },
  { path: '/:rest(.*)*', name: 'not-found', component: NotFound },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
})

// Research pages exist only with the research feature; without it they are
// not found, under their own URL, and so is the one-off form without the
// write grant. Users with research alone start there.
router.beforeEach(async (to) => {
  const research = isResearchPath(to.path)
  if (!research && to.path !== '/') return true
  await featuresLoaded
  if ((research && !hasFeature('research')) || (to.name === 'research-new' && !canStartResearch())) {
    return { name: 'not-found', params: { rest: to.path.slice(1).split('/') }, query: to.query, hash: to.hash }
  }
  if (to.path === '/' && !hasFeature('factory') && hasFeature('research')) return '/research'
  return true
})

createApp(App).use(router).mount('#app')
