import { defineConfig } from '@playwright/test';
import { assertPreviewTarget, assertRequiredEnvironment } from './apps/web-console/src/cloud/preview-target';
// Preview 真实环境验收配置：指向已部署的 b4.crane.youdaozd.com（真实 HTTPS 证书、
// 真实 Chrome GPU 渲染）。与 playwright.b4.config.ts（隔离 fixture、SwiftShader）
// 严格区分，不得互相冒充。双门禁 fail-closed，配置加载即报错、不启动浏览器：
//   1) A1_WEB_ORIGIN 必须严格等于 https://b4.crane.youdaozd.com；
//   2) B4_REQUIRED_ENVIRONMENT 必须为 b4-preview（写入前再与服务端哨兵比对）。
const origin = assertPreviewTarget(process.env.A1_WEB_ORIGIN);
assertRequiredEnvironment(process.env.B4_REQUIRED_ENVIRONMENT);
export default defineConfig({
  testDir: 'tests/e2e/b4',
  workers: 1,
  fullyParallel: false,
  timeout: 180000,
  expect: { timeout: 20000 },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report/b4-preview' }]],
  use: {
    baseURL: origin,
    ignoreHTTPSErrors: false, // Preview 必须验证真实证书，不关闭校验
    channel: 'chrome',
    viewport: { width: 1280, height: 720 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
});
