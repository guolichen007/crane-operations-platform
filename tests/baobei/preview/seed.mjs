// G4-S1A Preview synthetic business data.
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
// G4-S1A-R2 integrity rule. PLAN below is the deterministic expected data
// set (name, creator, owner, final status, stage, cross-team duplicate). Before
// writing anything the admin account (global scope, includes unassigned
// drafts) reads every project carrying the tag and compares with PLAN:
//   * none            → seed normally;
//   * exact match     → write nothing, report "complete";
//   * anything else   → stop with a list of differences (missing, extra,
//                       wrong status/owner/creator/stage). Nothing is deleted,
//                       repaired, returned or overwritten. A partially seeded
//                       Preview database is rebuilt (after confirmation) and
//                       seeded again — no automatic repair.
// After seeding, the same comparison must pass, otherwise the run fails.
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const ORIGIN = process.env.BAOBEI_SEED_ORIGIN || '';
const MODE = process.env.BAOBEI_SEED_LOGIN || 'oidc';
const TAG = process.env.BAOBEI_SEED_TAG || '合成UAT';
const PREVIEW_ORIGIN = 'https://baobei-preview.youdaozd.com';
const DEV_SUBJECTS = { salesA: 'synthetic-a', salesB: 'synthetic-b', salesC: 'synthetic-c', manager: 'synthetic-manager', manager2: 'synthetic-manager-other', director: 'synthetic-director', admin: 'synthetic-admin' };
export const STAGES = ['L0_LEAD', 'L1_REQUIREMENT', 'L2_TECHNICAL', 'L3_SITE_SURVEY', 'L4_SOLUTION', 'L5_QUOTATION', 'L6_TENDER', 'L7_COMMERCIAL', 'L8_PENDING_CONTRACT', 'WON', 'LOST', 'HOLD'];

// Deterministic expected data. key: unique id; creator/owner: account keys
// (owner null = unassigned). status: final intakeStatus; stage: final salesStage.
export function plan(tag = TAG) {
  const p = STAGES.map((st, i) => ({ key: `stage${i + 1}`, name: `${tag}·阶段样例${String(i + 1).padStart(2, '0')}`, creator: 'salesA', owner: 'salesA', status: 'DRAFT', stage: st }));
  p.push(
    { key: 'approved', name: `${tag}·东港钢铁桥机改造`, creator: 'salesA', owner: 'salesA', status: 'APPROVED', stage: 'L2_TECHNICAL' },
    { key: 'returned', name: `${tag}·北岭重工铸造车间`, creator: 'salesA', owner: 'salesA', status: 'RETURNED', stage: 'L2_TECHNICAL' },
    { key: 'original', name: `${tag}·西川港务岸桥`, creator: 'salesA', owner: 'salesA', status: 'SUBMITTED', stage: 'L3_SITE_SURVEY' },
    { key: 'crossTeamDuplicate', name: `${tag}·西川港务岸桥`, creator: 'salesC', owner: 'salesC', status: 'UNDER_REVIEW', stage: 'L3_SITE_SURVEY' },
    { key: 'teamQueue', name: `${tag}·中原电厂检修吊`, creator: 'salesB', owner: 'salesB', status: 'SUBMITTED', stage: 'L5_QUOTATION' },
    { key: 'unassigned', name: `${tag}·待分配·青石矿业`, creator: 'admin', owner: null, status: 'DRAFT', stage: 'L0_LEAD' },
  );
  return p;
}

// Pure comparison (unit-tested). found: [{id,projectName,ownerSubject,createdBySubject,intakeStatus,salesStage}]
// subjects: account key → subject. Returns {state:'empty'|'complete'|'inconsistent', problems:[...]}.
export function compareWithPlan(found, expected, subjects) {
  if (!found.length) return { state: 'empty', problems: [] };
  const problems = [];
  const pool = [...found];
  for (const e of expected) {
    const owner = e.owner ? subjects[e.owner] : '';
    const creator = subjects[e.creator];
    const candidates = pool.filter((f) => f.projectName === e.name && f.createdBySubject === creator);
    if (!candidates.length) {
      problems.push(`missing ${e.key} (${e.name}, creator ${e.creator})`);
      continue;
    }
    if (candidates.length > 1) problems.push(`duplicate ${e.key}: ${candidates.length} rows`);
    const f = candidates[0];
    pool.splice(pool.indexOf(f), 1);
    if ((f.ownerSubject || '') !== (owner || '')) problems.push(`owner ${e.key}: ${f.ownerSubject || 'none'} ≠ ${e.owner || 'none'}`);
    if (f.intakeStatus !== e.status) problems.push(`status ${e.key}: ${f.intakeStatus} ≠ ${e.status}`);
    if (f.salesStage !== e.stage) problems.push(`stage ${e.key}: ${f.salesStage} ≠ ${e.stage}`);
  }
  for (const f of pool) problems.push(`unexpected tagged project ${f.registrationNo || f.id} (${f.projectName})`);
  return { state: problems.length ? 'inconsistent' : 'complete', problems };
}

export function checkTarget(origin, mode, authConfig, tag = TAG) {
  if (!/^[A-Za-z0-9_一-鿿-]{2,24}$/.test(tag)) throw Error('invalid tag');
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
// Admin read of every tagged project (global scope, includes unassigned drafts).
async function tagged(admin) {
  const out = [];
  for (let page = 1; ; page++) {
    const r = await api(admin, `/api/v1/projects?pageSize=100&page=${page}&q=${encodeURIComponent(TAG)}`);
    out.push(...r.items.filter((x) => x.projectName.startsWith(TAG + '·')));
    if (page * 100 >= r.total) return out;
  }
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
  // Test hook (loopback dev stack only): stop after N writes to simulate an interrupted first run.
  const failAfter = MODE === 'dev' ? Number(process.env.BAOBEI_SEED_FAIL_AFTER || 0) : 0;
  let writes = 0;
  const accounts = MODE === 'oidc' ? JSON.parse(readFileSync(process.env.BAOBEI_SEED_ACCOUNTS_FILE, 'utf8')) : {};
  const require = createRequire(new URL('../../../apps/project-intake-web/package.json', import.meta.url));
  const { chromium } = require('@playwright/test');
  const browser = await chromium.launch();
  const out = { tag: TAG, origin: ORIGIN, created: [] };
  try {
    const pages = {};
    const as = async (key) => (pages[key] ??= await session(browser, key, accounts));
    const subjects = {};
    for (const key of Object.keys(DEV_SUBJECTS)) subjects[key] = (await api(await as(key), '/api/v1/me')).subject;
    const admin = await as('admin');
    const expected = plan();
    const before = compareWithPlan(await tagged(admin), expected, subjects);
    if (before.state === 'complete') {
      out.state = 'complete';
      out.skipped = `tag ${TAG}: all ${expected.length} expected projects present and consistent; nothing written`;
      return out;
    }
    if (before.state === 'inconsistent') {
      const e = Error(`tag ${TAG} is partially present or inconsistent; nothing written. Rebuild the isolated Preview database (after confirmation) and seed again:\n  ` + before.problems.join('\n  '));
      e.problems = before.problems;
      throw e;
    }
    const write = async (fn) => {
      if (failAfter && writes >= failAfter) throw Error(`simulated interruption after ${writes} writes`);
      writes++;
      return fn();
    };
    const create = (p, body) => write(async () => { const c = await api(p, '/api/v1/projects', 'POST', body); out.created.push(c.registrationNo || c.id); return c; });
    const submit = (p, c) => write(() => api(p, `/api/v1/projects/${c.id}/submit`, 'POST', { version: c.version }));
    const a = await as('salesA');
    // Sales A: drafts across all 12 stages with known / unknown / zero amounts.
    for (const [i, st] of STAGES.entries())
      await create(a, doc(`阶段样例${String(i + 1).padStart(2, '0')}`, `阶段客户${i + 1}`, st, i % 3 === 0 ? unknown : i % 3 === 1 ? known(100000 * (i + 1) + 0.5) : known(0)));
    // Sales A: three formal filings for review (approve / return / cross-team duplicate).
    const toApprove = await submit(a, await create(a, doc('东港钢铁桥机改造', '东港钢铁', 'L1_REQUIREMENT', known(1250000.5))));
    const toReturn = await submit(a, await create(a, doc('北岭重工铸造车间', '北岭重工', 'L2_TECHNICAL', unknown)));
    const original = await submit(a, await create(a, doc('西川港务岸桥', '西川港务', 'L3_SITE_SURVEY', known(3480000))));
    // Manager (team one): approve one, return one.
    const m = await as('manager');
    await write(() => api(m, `/api/v1/projects/${toApprove.id}/approve`, 'POST', { version: toApprove.version, reason: '合成：资料齐全，批准' }));
    await write(() => api(m, `/api/v1/projects/${toReturn.id}/return`, 'POST', { version: toReturn.version, reason: '合成：请补充现场照片' }));
    // Sales C (team two): same customer and project as team one's filing → cross-team duplicate, pending director.
    const c = await as('salesC');
    const dup = await submit(c, await create(c, doc('西川港务岸桥', '西川港务', 'L3_SITE_SURVEY', known(3500000))));
    out.crossTeamDuplicate = { registrationNo: dup.registrationNo, intakeStatus: dup.intakeStatus, of: original.registrationNo };
    // Sales B: one plain submitted filing waiting in team one's queue.
    const b = await as('salesB');
    await submit(b, await create(b, doc('中原电厂检修吊', '中原电厂', 'L5_QUOTATION', known(860000))));
    // Admin: an unassigned draft (admin custody only).
    await create(admin, { projectName: `${TAG}·待分配·青石矿业`, customer: `${TAG}·青石矿业有限公司`, ...known(450000), version: 1 });
    // Stage progression on the approved filing (L1 → L2) by its owner.
    const cur = await api(a, `/api/v1/projects/${toApprove.id}`);
    await write(() => api(a, `/api/v1/projects/${toApprove.id}/stage-change`, 'POST', { version: cur.version, salesStage: 'L2_TECHNICAL', reason: '合成：进入技术交流' }));
    // The result must match PLAN exactly.
    const after = compareWithPlan(await tagged(admin), expected, subjects);
    if (after.state !== 'complete') throw Error('seeded data does not match the plan:\n  ' + after.problems.join('\n  '));
    out.state = 'seeded';
    out.writes = writes;
    // Summary as seen by each role (scope check for the operator).
    out.scope = {};
    for (const key of ['salesA', 'manager', 'director', 'admin']) {
      const w = await api(await as(key), '/api/v1/workspace-summary');
      out.scope[key] = { total: w.counts.total, reviewable: w.counts.reviewable, unassignedDrafts: w.counts.unassignedDrafts };
    }
    return out;
  } finally {
    await browser.close();
  }
}

if (process.argv[1]?.endsWith('seed.mjs')) {
  seed().then((o) => console.log(JSON.stringify(o, null, 1))).catch((e) => { console.error('SEED FAILED:', e.message); process.exit(1); });
}
