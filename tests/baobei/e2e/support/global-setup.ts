import { chromium, type FullConfig } from '../../../../apps/project-intake-web/node_modules/@playwright/test/index.mjs';
// G3-4-A1 start-of-run check. Two failure modes are handled differently:
//
// * An ordinary failure (assertion, timeout) inside a test: the `cleanup`
//   fixture (isolation.ts) returns what that test submitted, in finally.
// * A killed run (SIGKILL, CI cancel, OOM): no teardown runs at all. This
//   setup does NOT modify such leftovers — they may belong to another run or
//   to someone's manual check. It lists them (id, number, status, name) and:
//     - by default continues: tests use unique run ids and page through the
//       queue (findCard), so leftovers cannot make them fail;
//     - with BAOBEI_E2E_REQUIRE_CLEAN=1 fails, asking for a rebuilt database
//       (CI starts every job on a fresh PostgreSQL service container; locally
//       drop and recreate the loopback test database).
// Only runs against a development dev-login stack.
export default async function globalSetup(config: FullConfig) {
  process.env.BAOBEI_E2E_RUN ??= Date.now().toString(36).slice(-5) + Math.random().toString(36).slice(2, 5);
  console.log(`[e2e] run id ${process.env.BAOBEI_E2E_RUN}`);
  const baseURL = (config.projects[0]?.use?.baseURL as string) || 'http://127.0.0.1:5188';
  const cfg = await fetch(baseURL + '/auth/config').then((r) => r.json()).catch(() => null);
  if (!cfg?.development) {
    console.log('[e2e-check] skipped: not a development dev-login stack');
    return;
  }
  const browser = await chromium.launch();
  try {
    const p = await (await browser.newContext({ baseURL })).newPage();
    await p.goto('/login');
    await p.locator('select').selectOption('synthetic-admin');
    await p.locator('[data-action-id="auth.dev-login"]:visible').click();
    await p.getByRole('heading', { name: '工作台', exact: true }).waitFor();
    const left: { id: string; registrationNo: string; intakeStatus: string; projectName: string }[] = [];
    for (let page = 1; ; page++) {
      const r = await p.evaluate(async (u) => (await fetch(u)).json(), `/api/v1/projects?review=pending&pageSize=100&page=${page}`);
      left.push(...r.items);
      if (page * 100 >= r.total) break;
    }
    if (!left.length) {
      console.log('[e2e-check] review queue clean');
      return;
    }
    const list = left.map((x) => `  ${x.id} ${x.registrationNo} ${x.intakeStatus} ${x.projectName.slice(0, 60)}`).join('\n');
    const msg = `[e2e-check] ${left.length} pending filing(s) exist before this run (left by an interrupted run or created outside it); not modified:\n${list}`;
    if (process.env.BAOBEI_E2E_REQUIRE_CLEAN === '1')
      throw new Error(msg + '\nBAOBEI_E2E_REQUIRE_CLEAN=1: rebuild the e2e database (fresh service container in CI; drop/create the loopback test DB locally) and rerun.');
    console.log(msg);
  } finally {
    await browser.close();
  }
}
