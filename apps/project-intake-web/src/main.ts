import Reviews from './Reviews.vue';
import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { createRouter, createWebHistory } from 'vue-router';
import App from './App.vue';
import Login from './Login.vue';
import Dashboard from './Dashboard.vue';
import Projects from './Projects.vue';
import Pipeline from './Pipeline.vue';
import Statistics from './Statistics.vue';
import Wizard from './Wizard.vue';
import Detail from './Detail.vue';
import StaffMembers from './StaffMembers.vue';
import StaffTeams from './StaffTeams.vue';
import { useApp } from './store';
import { routeAllowed } from './navigation';
import { loginErrorMessage } from './login-errors';
import { clearStaffRoleDraft, readStaffRoleDraft } from './staff-role-draft';
import './style.css';
const pinia = createPinia();
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/team/members', component: StaffMembers },
    { path: '/team/members/:id', component: StaffMembers },
    { path: '/settings/teams', component: StaffTeams },
    { path: '/settings/users', component: StaffMembers },
    { path: '/reviews', component: Reviews },
    { path: '/duplicates', component: Reviews },
    { path: '/login', component: Login },
    { path: '/', component: Dashboard },
    { path: '/projects', component: Projects },
    { path: '/unassigned', component: Projects },
    { path: '/followups', component: Projects },
    { path: '/pipeline', component: Pipeline },
    { path: '/statistics', component: Statistics },
    { path: '/new', component: Wizard },
    { path: '/projects/:id/edit', component: Wizard },
    { path: '/projects/:id', component: Detail },
  ],
});
router.beforeEach(async (to) => {
  const app = useApp(pinia);
  if (!app.sessionChecked) await app.loadUser();
  if (!app.user && to.path !== '/login') return { path: '/login', replace: true };
  if (app.user && to.path === '/login' && !loginErrorMessage(to.query.error)) return '/';
  if (app.user && to.path === '/') {
    const draft = readStaffRoleDraft();
    if (draft && app.user.role === 'baobei_admin' && draft.actor === app.user.subject)
      return { path: '/team/members/' + draft.staffId, replace: true };
    if (draft) clearStaffRoleDraft();
  }
  // Continuing an existing draft is authorized per project by Project.actions.
  if (to.path === '/new' && to.query.draft) return;
  if (app.user && to.path !== '/login' && !routeAllowed(app.user, to.path)) return '/';
});
createApp(App).use(pinia).use(router).mount('#app');
