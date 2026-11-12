import { defineConfig } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
export default defineConfig({
  testDir: '.', workers: 1, retries: 0, timeout: 60000, reporter: 'list',
  outputDir: '../../../local-private/baobei/keycloak-browser-results',
  use: { baseURL: process.env.BAOBEI_AUTH_FIXTURE_URL, viewport: { width: 1366, height: 860 }, locale: 'zh-CN' },
});
