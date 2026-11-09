import { defineConfig } from '../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-4-A1 executable UAT (docs/项目报备中心/34 §3). Not part of CI.
export default defineConfig({
  testDir: '.',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60000,
  reporter: 'list',
  outputDir: '../../../local-private/baobei/g34a1/uat-results',
  globalSetup: '../e2e/support/global-setup.ts',
  use: { baseURL: 'http://127.0.0.1:5188', trace: 'retain-on-failure' },
  projects: [{ name: 'desktop', use: { viewport: { width: 1440, height: 900 } } }],
});
