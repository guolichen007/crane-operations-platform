import { test, expect } from '@playwright/test';
test('offline/Demo Alarm retains explicit Mock data and local confirmation only', async ({
  page,
}) => {
  let cloudAlarmRequests = 0;
  page.on('request', (request) => {
    if (request.url().includes('/api/v1/alarms')) cloudAlarmRequests++;
  });
  await page.goto('/#/alarms');
  await expect(page.getByText('DEMO MOCK', { exact: true })).toBeVisible();
  await expect(page.getByText('DEMO · 本地演示工作区', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '确认告警', exact: true }).first()).toBeVisible();
  await page.getByRole('button', { name: '确认告警', exact: true }).first().click();
  await expect(page.getByRole('button', { name: '已确认', exact: true }).first()).toBeVisible();
  expect(cloudAlarmRequests).toBe(0);
});
