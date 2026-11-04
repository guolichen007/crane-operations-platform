import base from './playwright.config';

export default {
  ...base,
  testDir: './auth-e2e',
  outputDir: '../../local-private/baobei/auth-playwright-results',
  use: {
    ...base.use,
    baseURL: process.env.BAOBEI_AUTH_FIXTURE_URL,
    ignoreHTTPSErrors: true,
  },
};
