import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { createRouter, createWebHashHistory } from 'vue-router';
import App from './App.vue';
const Overview = () => import('./views/Overview.vue');
const Workspace = () => import('./views/Workspace.vue');
const CloudDevices = () => import('./views/CloudDevices.vue');
const InventoryOverview = () => import('./views/InventoryOverview.vue');
const InventoryProjects = () => import('./views/InventoryProjects.vue');
const InventoryDevices = () => import('./views/InventoryDevices.vue');
const AlarmCenter = () => import('./views/AlarmCenter.vue');
const cloudMode = import.meta.env.VITE_DATA_MODE === 'api';
const productionAuth = import.meta.env.VITE_AUTH_MODE === 'oidc';
import './styles.css';
import './refinements.css';
const routes = ['projects', 'devices', 'monitor', 'alarms', 'analytics', 'maintenance', 'remote']
  .filter((path) => !productionAuth || ['projects', 'devices', 'alarms'].includes(path))
  .map((path) => ({
    path: `/${path}`,
    component:
      cloudMode && path === 'devices'
        ? InventoryDevices
        : cloudMode && path === 'alarms'
          ? AlarmCenter
          : cloudMode && path === 'projects'
            ? InventoryProjects
            : Workspace,
  }));
createApp(App)
  .use(createPinia())
  .use(
    createRouter({
      history: createWebHashHistory(),
      routes: [
        { path: '/', redirect: '/overview' },
        { path: '/overview', component: cloudMode ? InventoryOverview : Overview },
        { path: '/runtime', component: CloudDevices },
        ...routes,
        { path: '/devices/:id', component: CloudDevices },
        ...(productionAuth ? [] : [{ path: '/system', component: Workspace }]),
        { path: '/:pathMatch(.*)*', redirect: '/overview' },
      ],
    })
  )
  .mount('#app');

import './visual-v031.css';
import './home-v033.css';
import './brand.css';
import './design/tokens.css';
import './premium.css';
