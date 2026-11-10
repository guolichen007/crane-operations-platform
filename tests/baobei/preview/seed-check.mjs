// G4-S1A-R2: deterministic checks of the seed integrity rule (pure, no network).
// Run by tests/baobei/preview.mjs (CI compose job) and directly with node.
import assert from 'node:assert/strict';
import { plan, compareWithPlan, checkTarget } from './seed.mjs';

const subjects = { salesA: 'sa', salesB: 'sb', salesC: 'sc', manager: 'm1', manager2: 'm2', director: 'd', admin: 'ad' };
const expected = plan('合成UAT');
const full = () =>
  expected.map((e, i) => ({ id: 'id' + i, registrationNo: 'BB-' + i, projectName: e.name, createdBySubject: subjects[e.creator], ownerSubject: e.owner ? subjects[e.owner] : '', intakeStatus: e.status, salesStage: e.stage }));
const results = {};
const check = (label, found, state, includes = []) => {
  const r = compareWithPlan(found, expected, subjects);
  assert.equal(r.state, state, label + ': ' + JSON.stringify(r.problems));
  for (const s of includes) assert(r.problems.some((p) => p.includes(s)), `${label}: expected a problem mentioning "${s}", got ${JSON.stringify(r.problems)}`);
  results[label] = r.state + (r.problems.length ? ` (${r.problems.length}: ${r.problems[0]}${r.problems.length > 1 ? ' …' : ''})` : '');
};

assert.equal(expected.length, 18);
assert.equal(new Set(expected.map((e) => e.key)).size, 18);
check('empty → seed', [], 'empty');
check('complete → no write', full(), 'complete');
check('order independent', full().reverse(), 'complete');
for (const n of [1, 2, 3]) check(`interrupted after ${n} create(s)`, full().slice(0, n), 'inconsistent', ['missing']);
check('cross-team duplicate missing', full().filter((f) => f.createdBySubject !== 'sc'), 'inconsistent', ['missing crossTeamDuplicate']);
check('approval not applied (still SUBMITTED)', full().map((f) => (f.projectName.endsWith('东港钢铁桥机改造') ? { ...f, intakeStatus: 'SUBMITTED', salesStage: 'L1_REQUIREMENT' } : f)), 'inconsistent', ['status approved', 'stage approved']);
check('return not applied', full().map((f) => (f.projectName.endsWith('北岭重工铸造车间') ? { ...f, intakeStatus: 'SUBMITTED' } : f)), 'inconsistent', ['status returned']);
check('unassigned draft was assigned', full().map((f) => (f.projectName.includes('待分配') ? { ...f, ownerSubject: 'sa' } : f)), 'inconsistent', ['owner unassigned']);
check('owner changed (handoff)', full().map((f) => (f.projectName.endsWith('中原电厂检修吊') ? { ...f, ownerSubject: 'sa' } : f)), 'inconsistent', ['owner teamQueue']);
check('duplicate created twice', [...full(), full()[0]], 'inconsistent', ['duplicate stage1']);
check('unexpected tagged project', [...full(), { id: 'x', registrationNo: 'BB-X', projectName: '合成UAT·人工新增', createdBySubject: 'sa', ownerSubject: 'sa', intakeStatus: 'DRAFT', salesStage: 'L0_LEAD' }], 'inconsistent', ['unexpected']);
check('same name by another creator', full().map((f) => (f.createdBySubject === 'sc' ? { ...f, createdBySubject: 'sb', ownerSubject: 'sb' } : f)), 'inconsistent', ['missing crossTeamDuplicate', 'unexpected']);

// Target guard.
const refuse = (o, m, cfg, tag) => assert.throws(() => checkTarget(o, m, cfg, tag));
refuse('https://baobei.youdaozd.com', 'oidc', { development: false, oidc: true });
refuse('https://baobei-preview.youdaozd.com', 'oidc', { development: true, oidc: false });
refuse('https://baobei-preview.youdaozd.com', 'dev', { development: true });
refuse('http://10.0.0.5:5188', 'dev', { development: true });
refuse('http://127.0.0.1:5188', 'oidc', { development: true });
refuse('http://127.0.0.1:5188', 'dev', { development: false });
refuse('http://127.0.0.1:5188', 'dev', { development: true }, 'x y;');
checkTarget('https://baobei-preview.youdaozd.com', 'oidc', { development: false, oidc: true }, '合成UAT');
checkTarget('http://127.0.0.1:5188', 'dev', { development: true }, '合成UAT');

for (const [k, v] of Object.entries(results)) console.log(`SEED_INTEGRITY ${k}: ${v}`);
console.log(`SEED_INTEGRITY_CASES=${Object.keys(results).length} PASS; SEED_TARGET_GUARD=PASS`);
