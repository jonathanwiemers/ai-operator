import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import { canStartResearch, featuresLoaded, hasFeature, isResearchPath } from './api'
import Stream from './views/Stream.vue'
import Inbox from './views/Inbox.vue'
import Project from './views/Project.vue'
import TaskView from './views/Task.vue'
import MilestoneView from './views/Milestone.vue'
import Researches from './views/Researches.vue'
import ResearchNew from './views/ResearchNew.vue'
import ResearchView from './views/Research.vue'
import ResearchRunsView from './views/ResearchRuns.vue'
import ResearchRunView from './views/ResearchRun.vue'
import NotFound from './views/NotFound.vue'
import './style.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'everything', component: Stream },
    { path: '/inbox', name: 'inbox', component: Inbox },
    { path: '/projects/:namespace/:name', name: 'stream-project', component: Stream, props: true },
    {
      path: '/projects/:namespace/:name/milestones/:iid',
      name: 'stream-milestone',
      component: Stream,
      props: true,
    },
    { path: '/projects/:namespace/:name/board', name: 'project-board', component: Project, props: true },
    { path: '/projects/:namespace/:name/tasks/:task', name: 'task', component: TaskView, props: true },
    // Keep the old milestone page reachable for deep links that need the full
    // milestone UI; the stream is the default for /milestones/:iid above.
    {
      path: '/projects/:namespace/:name/milestones/:iid/detail',
      name: 'milestone',
      component: MilestoneView,
      props: true,
    },
    { path: '/research', name: 'researches', component: Researches },
    { path: '/research/new', name: 'research-new', component: ResearchNew },
    { path: '/research/:namespace/:name', name: 'research', component: ResearchView, props: true },
    { path: '/research/:namespace/:name/runs', name: 'research-runs', component: ResearchRunsView, props: true },
    { path: '/research/:namespace/:name/runs/:id', name: 'research-run', component: ResearchRunView, props: true },
    { path: '/:rest(.*)*', name: 'not-found', component: NotFound },
  ],
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
