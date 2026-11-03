import { createApp } from 'vue';
import { createPinia } from 'pinia';
import { createRouter, createWebHashHistory } from 'vue-router';
import { VueQueryPlugin } from '@tanstack/vue-query';
import App from './App.vue';
import { queryClient } from './cloud/queryClient';
const Overview = () => import('./views/Overview.vue');
const Workspace = () => import('./views/Workspace.vue');
const CloudDevices = () => import('./views/CloudDevices.vue');
const InventoryOverview = () => import('./views/InventoryOverview.vue');
const InventoryProjects = () => import('./views/InventoryProjects.vue');
const InventoryDevices = () => import('./views/InventoryDevices.vue');
const AlarmCenter = () => import('./views/AlarmCenter.vue');
const WorkspacePlaceholder = () => import('./views/WorkspacePlaceholder.vue');
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
  .use(VueQueryPlugin, { queryClient })
  .use(
    createRouter({
      history: createWebHashHistory(),
      routes: [
        { path: '/', redirect: '/overview' },
        { path: '/overview', component: cloudMode ? InventoryOverview : Overview },
        { path: '/runtime', component: CloudDevices },
        ...routes,
        { path: '/devices/:id', component: CloudDevices },
        {
          path: '/assets',
          component: WorkspacePlaceholder,
          props: { title: '资产中心', description: '资产台账 / 位置 / Equipment 运营工作区。' },
        },
        {
          path: '/portfolio',
          component: WorkspacePlaceholder,
          props: { title: '项目组合', description: '项目登记 / 资源 / 排期 / 冲突。' },
        },
        {
          path: '/locations',
          component: WorkspacePlaceholder,
          props: { title: '位置中心', description: '位置树 / 详情 / 资产。' },
        },
        {
          path: '/catalog',
          component: WorkspacePlaceholder,
          props: { title: '设备目录', description: '设备类型 / 制造商 / 型号目录。' },
        },
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
