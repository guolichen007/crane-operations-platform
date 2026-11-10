// G4-S1A Preview synthetic business data. Repeatable and idempotent: every
// record carries the tag (default 合成UAT); if the tag already exists for the
// first sales account, nothing is written again.
//
// Writes only through the real HTTP API (normal permission checks, audit,
// review independence). No direct SQL, no DEV_AUTH in Preview.
//
//   Preview (OIDC):  BAOBEI_SEED_ORIGIN=https://baobei-preview.youdaozd.com \
//                    BAOBEI_SEED_ACCOUNTS_FILE=/srv/u-dao/secrets/baobei-preview/seed-accounts.json \
//                    node tests/baobei/preview/seed.mjs
//     seed-accounts.json: {"salesA":{"username":"…","password":"…"}, "salesB":…,
//       "salesC":…, "manager":…, "manager2":…, "director":…, "admin":…}
//     (synthetic Keycloak accounts of the Preview realm; never real staff)
//   Local check only: BAOBEI_SEED_LOGIN=dev BAOBEI_SEED_ORIGIN=http://127.0.0.1:5188
//
// Covers: four roles, cross-team duplicate (pending director decision),
// review approve and return, unassigned draft (admin), all 12 sales stages
// represented across drafts, amounts known / unknown / known-zero.
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
const require = createRequire(new URL('../../../apps/project-intake-web/package.json', import.meta.url));
const { chromium } = require('@playwright/test');

const ORIGIN = process.env.BAOBEI_SEED_ORIGIN || '';
const MODE = process.env.BAOBEI_SEED_LOGIN || 'oidc';
const TAG = process.env.BAOBEI_SEED_TAG || '合成UAT';
const PREVIEW_ORIGIN = 'https://baobei-preview.youdaozd.com';
const DEV_SUBJECTS = { salesA: 'synthetic-a', salesB: 'synthetic-b', salesC: 'synthetic-c', manager: 'synthetic-manager', manager2: 'synthetic-manager-other', director: 'synthetic-director', admin: 'synthetic-admin' };

export function checkTarget(origin, mode, authConfig) {
  if (!/^[A-Za-z0-9_一-鿿-]{2,24}$/.test(TAG)) throw Error('invalid tag');
  if (mode === 'oidc') {
    if (origin !== PREVIEW_ORIGIN) throw Error(`refusing: OIDC seeding only targets ${PREVIEW_ORIGIN}`);
    if (!authConfig || authConfig.development !== false || authConfig.oidc !== true) throw Error('refusing: target is not an OIDC (non-development) stack');
  } else if (mode === 'dev') {
    const u = new URL(origin);
    if (u.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(u.hostname)) throw Error('refusing: dev login only on a loopback synthetic stack');
    if (!authConfig?.development) throw Error('refusing: dev login unavailable');
  } else throw Error('BAOBEI_SEED_LOGIN must be oidc or dev');
}

async function session(browser, key, accounts) {
  const page = await (await browser.newContext({ baseURL: ORIGIN })).newPage();
  if (MODE === 'dev') {
    await page.goto('/login');
    await page.locator('select').selectOption(DEV_SUBJECTS[key]);
    await page.locator('[data-action-id="auth.dev-login"]:visible').click();
  } else {
    const acc = accounts[key];
    if (!acc?.username || !acc?.password) throw Error('missing synthetic account ' + key);
    await page.goto('/auth/login');
    // Standard Keycloak login form.
    await page.locator('#username').fill(acc.username);
    await page.locator('#password').fill(acc.password);
    await page.locator('#kc-login').click();
  }
  await page.getByRole('heading', { name: '工作台', exact: true }).waitFor({ timeout: 30000 });
  return page;
}
async function api(page, path, method = 'GET', body) {
  const r = await page.evaluate(async ({ path, method, body }) => {
    const me = await (await fetch('/api/v1/me')).json();
    const res = await fetch(path, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': me.csrfToken, 'Idempotency-Key': 'seed-' + crypto.randomUUID() },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    return { status: res.status, body: await res.json().catch(() => null) };
  }, { path, method, body });
  if (r.status >= 300) throw Error(`${method} ${path} → ${r.status} ${r.body?.code ?? ''}`);
  return r.body;
}
const doc = (name, customer, stage, extra = {}) => ({
  projectName: `${TAG}·${name}`,
  customer: `${TAG}·${customer}有限公司`,
  location: { province: '合成省', city: '合成市', factoryName: `${customer}合成厂区`, workshop: '主厂房' },
  projectType: 'RETROFIT', salesStage: stage, informationSource: 'END_CUSTOMER', confidenceLevel: 'CUSTOMER_CONFIRMED',
  requirements: ['ANTI_SWAY'], requirementNotes: '合成需求：桥机防摇与定位（Preview 测试数据）',
  plannedEquipment: [{ equipmentType: 'BRIDGE', quantity: 2, capacityTons: 32 }],
  ...extra,
});
const known = (v) => ({ estimatedAmount: v, knowledge: { estimatedAmount: 'KNOWN' } });
const unknown = { estimatedAmount: null };

export async function seed() {
  const authConfig = await fetch(ORIGIN + '/auth/config').then((r) => r.json()).catch(() => null);
  checkTarget(ORIGIN, MODE, authConfig);
  const accounts = MODE === 'oidc' ? JSON.parse(readFileSync(process.env.BAOBEI_SEED_ACCOUNTS_FILE, 'utf8')) : {};
  const browser = await chromium.launch();
  const out = { tag: TAG, origin: ORIGIN, created: [] };
  try {
    const a = await session(browser, 'salesA', accounts);
    const existing = await api(a, '/api/v1/projects?pageSize=1&q=' + encodeURIComponent(TAG));
    if (existing.total > 0) {
      out.skipped = `tag ${TAG} already present (${existing.total} visible to sales A); nothing written`;
      return out;
    }
    const create = async (p, body) => { const c = await api(p, '/api/v1/projects', 'POST', body); out.created.push(c.registrationNo || c.id); return c; };
    const submit = (p, c) => api(p, `/api/v1/projects/${c.id}/submit`, 'POST', { version: c.version });
    // Sales A: drafts across all 12 stages with known / unknown / zero amounts.
    const stages = ['L0_LEAD', 'L1_REQUIREMENT', 'L2_TECHNICAL', 'L3_SITE_SURVEY', 'L4_SOLUTION', 'L5_QUOTATION', 'L6_TENDER', 'L7_COMMERCIAL', 'L8_PENDING_CONTRACT', 'WON', 'LOST', 'HOLD'];
    for (const [i, st] of stages.entries())
      await create(a, doc(`阶段样例${String(i + 1).padStart(2, '0')}`, `阶段客户${i + 1}`, st, i % 3 === 0 ? unknown : i % 3 === 1 ? known(100000 * (i + 1) + 0.5) : known(0)));
    // Sales A: three formal filings for review (approve / return / cross-team duplicate).
    const toApprove = await submit(a, await create(a, doc('东港钢铁桥机改造', '东港钢铁', 'L1_REQUIREMENT', known(1250000.5))));
    const toReturn = await submit(a, await create(a, doc('北岭重工铸造车间', '北岭重工', 'L2_TECHNICAL', unknown)));
    const original = await submit(a, await create(a, doc('西川港务岸桥', '西川港务', 'L3_SITE_SURVEY', known(3480000))));
    // Manager (team one): approve one, return one.
    const m = await session(browser, 'manager', accounts);
    await api(m, `/api/v1/projects/${toApprove.id}/approve`, 'POST', { version: toApprove.version, reason: '合成：资料齐全，批准' });
    await api(m, `/api/v1/projects/${toReturn.id}/return`, 'POST', { version: toReturn.version, reason: '合成：请补充现场照片' });
    // Sales C (team two): same customer and project as team one's filing → cross-team duplicate, pending director.
    const c = await session(browser, 'salesC', accounts);
    const dup = await submit(c, await create(c, doc('西川港务岸桥', '西川港务', 'L3_SITE_SURVEY', known(3500000))));
    out.crossTeamDuplicate = { registrationNo: dup.registrationNo, intakeStatus: dup.intakeStatus, of: original.registrationNo };
    // Sales B: one plain submitted filing waiting in team one's queue.
    const b = await session(browser, 'salesB', accounts);
    await submit(b, await create(b, doc('中原电厂检修吊', '中原电厂', 'L5_QUOTATION', known(860000))));
    // Admin: an unassigned draft (admin custody only).
    const ad = await session(browser, 'admin', accounts);
    await create(ad, { projectName: `${TAG}·待分配·青石矿业`, customer: `${TAG}·青石矿业有限公司`, ...known(450000), version: 1 });
    // Stage progression on the approved filing (L1 → L2) by its owner.
    const cur = await api(a, `/api/v1/projects/${toApprove.id}`);
    await api(a, `/api/v1/projects/${toApprove.id}/stage-change`, 'POST', { version: cur.version, salesStage: 'L2_TECHNICAL', reason: '合成：进入技术交流' });
    // Summary as seen by each role (scope check for the operator).
    out.scope = {};
    for (const [key, p] of Object.entries({ salesA: a, manager: m, director: await session(browser, 'director', accounts), admin: ad })) {
      const w = await api(p, '/api/v1/workspace-summary');
      out.scope[key] = { total: w.counts.total, reviewable: w.counts.reviewable, unassignedDrafts: w.counts.unassignedDrafts };
    }
    return out;
  } finally {
    await browser.close();
  }
}

if (import.meta.url === `file://${process.argv[1]}` || process.argv[1]?.endsWith('seed.mjs')) {
  seed().then((o) => console.log(JSON.stringify(o, null, 1))).catch((e) => { console.error('SEED FAILED:', e.message); process.exit(1); });
}
