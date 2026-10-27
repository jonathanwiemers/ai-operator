import { createApp } from 'vue'
import { createRouter, createWebHistory } from 'vue-router'
import App from './App.vue'
import Projects from './views/Projects.vue'
import Project from './views/Project.vue'
import TaskView from './views/Task.vue'
import MilestoneView from './views/Milestone.vue'
import NotFound from './views/NotFound.vue'
import './style.css'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'projects', component: Projects },
    { path: '/projects/:namespace/:name', name: 'project', component: Project, props: true },
    { path: '/projects/:namespace/:name/tasks/:task', name: 'task', component: TaskView, props: true },
    { path: '/projects/:namespace/:name/milestones/:iid', name: 'milestone', component: MilestoneView, props: true },
    { path: '/:rest(.*)*', name: 'not-found', component: NotFound },
  ],
  scrollBehavior: (_to, _from, saved) => saved ?? { top: 0 },
})

createApp(App).use(router).mount('#app')
