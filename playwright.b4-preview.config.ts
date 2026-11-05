import { defineConfig } from '@playwright/test';
// Preview 真实环境验收配置：指向已部署的 b4.crane.youdaozd.com（真实 HTTPS 证书、
// 真实 Chrome GPU 渲染）。与 playwright.b4.config.ts（隔离 fixture、SwiftShader）
// 严格区分，不得互相冒充。
export default defineConfig({
  testDir: 'tests/e2e/b4',
  workers: 1,
  fullyParallel: false,
  timeout: 180000,
  expect: { timeout: 20000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report/b4-preview' }]],
  use: {
    baseURL: process.env.B4_PREVIEW_ORIGIN,
    ignoreHTTPSErrors: false, // Preview 必须验证真实证书，不关闭校验
    channel: 'chrome',
    viewport: { width: 1280, height: 720 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
});
