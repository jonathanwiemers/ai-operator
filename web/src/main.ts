import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import { featuresLoaded, hasFeature, isResearchPath } from './api'
import Projects from './views/Projects.vue'
import Project from './views/Project.vue'
import TaskView from './views/Task.vue'
import MilestoneView from './views/Milestone.vue'
import Researches from './views/Researches.vue'
import NotFound from './views/NotFound.vue'
import './style.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'projects', component: Projects },
    { path: '/projects/:namespace/:name', name: 'project', component: Project, props: true },
    { path: '/projects/:namespace/:name/tasks/:task', name: 'task', component: TaskView, props: true },
    { path: '/projects/:namespace/:name/milestones/:iid', name: 'milestone', component: MilestoneView, props: true },
    { path: '/research', name: 'researches', component: Researches },
    { path: '/:rest(.*)*', name: 'not-found', component: NotFound },
  ],
  scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
})

// Research pages exist only with the research feature; without it they are
// not found, under their own URL. Users with research alone start there.
router.beforeEach(async (to) => {
  const research = isResearchPath(to.path)
  if (!research && to.path !== '/') return true
  await featuresLoaded
  if (research && !hasFeature('research')) {
    return { name: 'not-found', params: { rest: to.path.slice(1).split('/') }, query: to.query, hash: to.hash }
  }
  if (to.path === '/' && !hasFeature('factory') && hasFeature('research')) return '/research'
  return true
})

createApp(App).use(router).mount('#app')
