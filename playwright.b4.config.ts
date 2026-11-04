import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: 'tests/e2e/b4',
  workers: 1,
  fullyParallel: false,
  timeout: 180000,
  expect: { timeout: 20000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report/b4' }]],
  use: {
    baseURL: process.env.A1_WEB_ORIGIN,
    ignoreHTTPSErrors: true,
    channel: process.env.PLAYWRIGHT_CHANNEL || (process.env.CI ? undefined : 'chrome'),
    viewport: { width: 1440, height: 900 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    launchOptions: {
      args: [
        '--host-resolver-rules=MAP crane.test 127.0.0.1,MAP auth.crane.test 127.0.0.1',
        '--no-proxy-server',
        '--enable-unsafe-swiftshader',
      ],
    },
  },
});
