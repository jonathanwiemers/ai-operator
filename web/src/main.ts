import { createApp } from 'vue'
import uiIcons from '@nuxt/ui/runtime/vue/plugins/icons.js'
import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import App from './App.vue'
import { canStartResearch, featuresLoaded, hasFeature, isResearchPath } from './api'
import { darkQuery, followColorScheme } from './colorScheme'
import type { RouteName } from './routeNames'
import { listenForSheets } from './ui/useBackClose'
import './style.css'

followColorScheme(document.documentElement.classList, window.matchMedia(darkQuery))

// Views load with their route, each as its own chunk. Only App.vue (the shell,
// with SignIn.vue) is in the initial bundle, which scripts/bundle-budget.mjs
// keeps within bundle-budget.json (docs/ui.md "Bundle budget").
const Dashboard = () => import('./views/Dashboard.vue')
const Stream = () => import('./views/Stream.vue')
const Inbox = () => import('./views/Inbox.vue')
const Costs = () => import('./views/Costs.vue')
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
  { path: '/costs', name: 'costs', component: Costs },
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

// Of Nuxt UI's Vue plugin (@nuxt/ui/vue-plugin) only the icons: the whole
// plugin would add the colour plugin, whose <style> the CSP refuses
// (style.css holds the colours instead), and an unhead instance nothing here
// uses (docs/ui.md, "UI kit"). Links need no plugin, they use vue-router.
createApp(App).use(router).use(uiIcons).mount('#app')
