import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { createRouter, createWebHashHistory } from 'vue-router';
import App from './App.vue';
import Overview from './views/Overview.vue';
import Workspace from './views/Workspace.vue';
import CloudDevices from './views/CloudDevices.vue';
const cloudMode = import.meta.env.VITE_DATA_MODE === 'api';
import './styles.css';
import './refinements.css';
const routes = [
  'projects',
  'devices',
  'monitor',
  'alarms',
  'analytics',
  'maintenance',
  'remote',
].map((path) => ({
  path: `/${path}`,
  component: path === 'devices' && cloudMode ? CloudDevices : Workspace,
}));
createApp(App)
  .use(createPinia())
  .use(
    createRouter({
      history: createWebHashHistory(),
      routes: [
        { path: '/', redirect: '/overview' },
        { path: '/overview', component: Overview },
        ...routes,
        { path: '/devices/:id', component: CloudDevices },
        { path: '/system', component: Workspace },
      ],
    })
  )
  .mount('#app');

import './visual-v031.css';
import './home-v033.css';
