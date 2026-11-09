import { chromium, type FullConfig } from '../../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
import { assertCleaned, returnPending } from './isolation';
// G3-4-A1 recovery. Tests return what they submit (cleanup fixture), but a run
// that is killed (SIGKILL, CI cancel, OOM) never reaches any teardown and can
// leave pending filings in front of the next run's targets. Before any test
// starts, nothing in the e2e review queue belongs to the new run, so every
// pending filing is a leftover: it is returned and listed by id.
//
// Guarded: only against a development API with dev login (the synthetic e2e
// stack). BAOBEI_E2E_RECOVER=0 disables it, e.g. to inspect a leftover or
// when one CI-order run is split into several invocations on one database.
export default async function globalSetup(config: FullConfig) {
  process.env.BAOBEI_E2E_RUN ??= Date.now().toString(36).slice(-5) + Math.random().toString(36).slice(2, 5);
  console.log(`[e2e] run id ${process.env.BAOBEI_E2E_RUN}`);
  if (process.env.BAOBEI_E2E_RECOVER === '0') return;
  const baseURL = (config.projects[0]?.use?.baseURL as string) || 'http://127.0.0.1:5188';
  const cfg = await fetch(baseURL + '/auth/config').then((r) => r.json()).catch(() => null);
  if (!cfg?.development) {
    console.log('[e2e-recover] skipped: not a development dev-login stack');
    return;
  }
  const browser = await chromium.launch();
  try {
    const ctx = await browser.newContext({ baseURL });
    const p = await ctx.newPage();
    await p.goto('/login');
    await p.locator('select').selectOption('synthetic-admin');
    await p.locator('[data-action-id="auth.dev-login"]:visible').click();
    await p.getByRole('heading', { name: '工作台', exact: true }).waitFor();
    const ids: string[] = [];
    for (let page = 1; ; page++) {
      const r = await p.evaluate(async (u) => (await fetch(u)).json(), `/api/v1/projects?review=pending&pageSize=100&page=${page}`);
      ids.push(...(r.items as { id: string }[]).map((x) => x.id));
      if (page * 100 >= r.total) break;
    }
    await ctx.close();
    if (!ids.length) {
      console.log('[e2e-recover] review queue clean');
      return;
    }
    console.log(`[e2e-recover] returning ${ids.length} pending filing(s) present before this run (interrupted or earlier run): ${ids.join(', ')}`);
    assertCleaned(await returnPending(browser, ids, 'global-setup recovery'), 'global-setup recovery');
  } finally {
    await browser.close();
  }
}
