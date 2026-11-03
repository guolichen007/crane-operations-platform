import { defineConfig } from '../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 45000,
  reporter: 'list',
  outputDir: '../../local-private/baobei/playwright-results',
  use: {
    baseURL: process.env.BAOBEI_BASE_URL || 'http://127.0.0.1:5188',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    channel: process.env.BAOBEI_BROWSER_CHANNEL || undefined,
  },
  projects: [
    { name: 'desktop', use: { viewport: { width: 1440, height: 900 } } },
    {
      name: 'mobile-390',
      use: { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true },
    },
  ],
});
