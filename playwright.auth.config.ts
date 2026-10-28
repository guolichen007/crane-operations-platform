import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: 'tests/e2e/auth', workers: 1, fullyParallel: false, timeout: 60000, expect: { timeout: 15000 }, reporter: 'list',
  use: { baseURL: process.env.A1_WEB_ORIGIN, ignoreHTTPSErrors: true, channel: process.env.PLAYWRIGHT_CHANNEL || (process.env.CI ? undefined : 'chrome'),
    viewport: { width: 1440, height: 900 }, trace: 'off', screenshot: 'off', video: 'off',
    launchOptions: { args: ['--host-resolver-rules=MAP crane.test 127.0.0.1,MAP auth.crane.test 127.0.0.1', '--no-proxy-server', '--enable-unsafe-swiftshader'] },
  },
});
