import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { createRouter, createWebHistory } from 'vue-router';
import App from './App.vue';
import Login from './Login.vue';
import Dashboard from './Dashboard.vue';
import Projects from './Projects.vue';
import Wizard from './Wizard.vue';
import Detail from './Detail.vue';
import { useApp } from './store';
import './style.css';
const pinia = createPinia();
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: Login },
    { path: '/', component: Dashboard },
    { path: '/projects', component: Projects },
    { path: '/followups', component: Projects },
    { path: '/pipeline', component: Projects },
    { path: '/statistics', component: Dashboard },
    { path: '/new', component: Wizard },
    { path: '/projects/:id/edit', component: Wizard },
    { path: '/projects/:id', component: Detail },
  ],
});
router.beforeEach(async (to) => {
  const app = useApp(pinia);
  if (!app.user) await app.loadUser();
  if (!app.user && to.path != '/login') return '/login';
  if (app.user && to.path === '/login') return '/';
  if ((to.path === '/new' || to.path.endsWith('/edit')) && app.user?.role !== 'baobei_sales')
    return '/';
});
createApp(App).use(pinia).use(router).mount('#app');
