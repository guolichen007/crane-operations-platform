// SEC-EX-001 negative cases (node --test). Inputs are the real reports of
// 661270f (fixtures) and the real package-lock/approval, mutated per case.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { evaluate, APPROVAL_SHA256 } from './audit-gate-core.mjs';

const here = new URL('.', import.meta.url);
const read = (p) => readFileSync(new URL(p, here), 'utf8');
const FULL = read('fixtures/npm-audit-full-661270f.json');
const PROD = read('fixtures/npm-audit-prod-661270f.json');
const LOCK = read('../../../apps/project-intake-web/package-lock.json');
const APPROVAL = read('sec-ex-001.json');
const REGISTRY = JSON.stringify({ '4.0.4': '2026-03-23T20:39:47.960Z', '4.0.5': '2026-07-02T16:47:23.026Z',
  '4.0.6': '2026-08-24T14:29:55.167Z', '4.0.7': '2026-08-24T14:36:23.690Z', modified: '2026-08-24T14:36:23.879Z' });
const NOW = new Date('2026-11-18T09:00:00Z');
const base = () => ({ full: FULL, prod: PROD, lock: LOCK, approval: APPROVAL, registry: REGISTRY, now: NOW });
const edit = (text, fn) => { const v = JSON.parse(text); fn(v); return JSON.stringify(v); };
const recount = (report) => {
  const counts = { info: 0, low: 0, moderate: 0, high: 0, critical: 0 };
  for (const v of Object.values(report.vulnerabilities)) counts[v.severity]++;
  report.metadata.vulnerabilities = { ...counts, total: Object.values(counts).reduce((a, b) => a + b, 0) };
};
const refused = (input, pattern) => {
  const r = evaluate(input);
  assert.equal(r.ok, false, 'accepted: ' + JSON.stringify(r));
  assert.match(r.reasons.join(' | '), pattern);
};

test('approval record is the reviewed one', () => {
  assert.equal(createHash('sha256').update(APPROVAL).digest('hex'), APPROVAL_SHA256);
});

test('real 661270f reports pass with the approved exception and name the unfixed vulnerability', () => {
  const r = evaluate(base());
  assert.equal(r.ok, true, JSON.stringify(r));
  assert.equal(r.status, 'PASS_WITH_APPROVED_EXCEPTION');
  assert.equal(r.unfixed.ghsa, 'GHSA-fc5q-39xc-vjpf');
  assert.equal(r.unfixed.cve, 'CVE-2026-112366');
  assert.equal(r.unfixed.deadline, '2026-12-18');
  assert.equal(r.unfixed.reported.length, 13);
});

test('another GHSA on a different package is refused', () => {
  refused({ ...base(), full: edit(FULL, (d) => {
    d.vulnerabilities.vite.via.push({ source: 999001, name: 'vite', dependency: 'vite', url: 'https://github.com/advisories/GHSA-xxxx-yyyy-zzzz', severity: 'high', range: '<8' });
  }) }, /not covered by SEC-EX-001/);
});

test('a second advisory on picomatch itself is refused', () => {
  refused({ ...base(), full: edit(FULL, (d) => {
    d.vulnerabilities.picomatch.via.push({ source: 999002, name: 'picomatch', dependency: 'picomatch', url: 'https://github.com/advisories/GHSA-aaaa-bbbb-cccc', severity: 'high', range: '<=4.0.7' });
  }) }, /advisories differ from the approved one/);
  refused({ ...base(), full: edit(FULL, (d) => { d.vulnerabilities.picomatch.via[0].range = '<=4.1.0'; }) }, /advisories differ/);
  refused({ ...base(), full: edit(FULL, (d) => { d.vulnerabilities.picomatch.via[0].url = 'https://github.com/advisories/GHSA-zzzz-zzzz-zzzz'; }) }, /advisories differ/);
});

test('a critical finding is refused even on the approved chain', () => {
  refused({ ...base(), full: edit(FULL, (d) => { d.vulnerabilities.picomatch.severity = 'critical'; recount(d); }) }, /critical/);
});

test('a production dependency finding is refused', () => {
  refused({ ...base(), prod: edit(PROD, (d) => {
    d.vulnerabilities.vue = { name: 'vue', severity: 'high', isDirect: true, via: [{ source: 1, name: 'vue', url: 'https://github.com/advisories/GHSA-prod-prod-prod', severity: 'high', range: '*' }], effects: [], range: '*', nodes: ['node_modules/vue'], fixAvailable: false };
    recount(d);
  }) }, /production dependency vue/);
  // the same chain marked as non-dev in the lock
  refused({ ...base(), lock: edit(LOCK, (d) => { delete d.packages['node_modules/vite/node_modules/picomatch'].dev; }) }, /not a dev dependency|dependency tree changed/);
});

test('expired approval is refused', () => {
  assert.equal(evaluate({ ...base(), now: new Date('2026-12-18T23:59:00Z') }).ok, true);
  refused({ ...base(), now: new Date('2026-12-19T00:00:01Z') }, /expired on 2026-12-18/);
});

test('an official fix shortens the exception to 7 days', () => {
  const fixed = edit(REGISTRY, (d) => { d['4.0.8'] = '2026-11-20T10:00:00Z'; });
  assert.equal(evaluate({ ...base(), registry: fixed, now: new Date('2026-11-27T12:00:00Z') }).ok, true);
  refused({ ...base(), registry: fixed, now: new Date('2026-11-28T00:00:01Z') }, /official fix picomatch@4\.0\.8/);
});

test('abnormal audit results are refused', () => {
  refused({ ...base(), full: '' }, /invalid JSON/);
  refused({ ...base(), full: '<html>rate limited</html>' }, /invalid JSON/);
  refused({ ...base(), full: JSON.stringify({ error: { code: 'ENOAUDIT', summary: 'registry unavailable' } }) }, /npm reported an error|missing vulnerabilities/);
  refused({ ...base(), full: edit(FULL, (d) => { d.auditReportVersion = 3; }) }, /auditReportVersion/);
  refused({ ...base(), full: edit(FULL, (d) => { d.metadata.vulnerabilities.high = 12; }) }, /metadata\.high/);
  refused({ ...base(), full: edit(FULL, (d) => { delete d.vulnerabilities.vite.nodes; }) }, /malformed entry vite/);
  refused({ ...base(), prod: 'null' }, /invalid JSON/);
  refused({ ...base(), registry: '{}' }, /registry/);
  refused({ ...base(), registry: 'npm ERR! network' }, /invalid JSON/);
});

test('mixed reports: approved chain plus an unrelated high finding are refused', () => {
  refused({ ...base(), full: edit(FULL, (d) => {
    d.vulnerabilities.ajv = { name: 'ajv', severity: 'high', isDirect: true, via: ['picomatch'], effects: [], range: '*', nodes: ['node_modules/ajv'], fixAvailable: false };
    recount(d);
  }) }, /ajv: not an approved propagated package/);
  refused({ ...base(), full: edit(FULL, (d) => {
    d.vulnerabilities.vitest.via = ['picomatch', 'tinyglobby', 'vite', 'esbuild'];
    d.vulnerabilities.esbuild = { name: 'esbuild', severity: 'moderate', isDirect: false, via: [{ source: 7, name: 'esbuild', url: 'https://github.com/advisories/GHSA-esb', severity: 'moderate', range: '*' }], effects: [], range: '*', nodes: ['node_modules/esbuild'], fixAvailable: false };
    recount(d);
  }) }, /vitest: does not resolve only to GHSA-fc5q-39xc-vjpf/);
});

test('forged or edited approvals are refused', () => {
  refused({ ...base(), approval: APPROVAL.replace('2026-12-18', '2027-12-18') }, /edited or forged/);
  refused({ ...base(), approval: APPROVAL.replace('"vite",', '"vite",\n    "esbuild",') }, /edited or forged/);
  refused({ ...base(), approval: JSON.stringify(JSON.parse(APPROVAL)) }, /edited or forged/);
  refused({ ...base(), approval: undefined }, /edited or forged/);
});

test('dependency tree changes are refused', () => {
  // a fifth picomatch copy
  refused({ ...base(), lock: edit(LOCK, (d) => {
    d.packages['node_modules/foo/node_modules/picomatch'] = { ...d.packages['node_modules/vite/node_modules/picomatch'] };
  }) }, /dependency tree changed/);
  // different version or integrity on an approved node
  refused({ ...base(), lock: edit(LOCK, (d) => { d.packages['node_modules/vite/node_modules/picomatch'].version = '4.0.6'; }) }, /dependency tree changed/);
  refused({ ...base(), lock: edit(LOCK, (d) => { d.packages['node_modules/vite/node_modules/picomatch'].integrity = 'sha512-forged'; }) }, /dependency tree changed/);
  // the report names a node the lock does not have
  refused({ ...base(), full: edit(FULL, (d) => { d.vulnerabilities.fdir.nodes.push('node_modules/ghost/node_modules/fdir'); }) }, /unknown node/);
  // the report's picomatch nodes differ from the approved list
  refused({ ...base(), full: edit(FULL, (d) => { d.vulnerabilities.picomatch.nodes.pop(); }) }, /reported nodes differ/);
});

test('a clean report passes without the exception and asks for its removal', () => {
  const clean = edit(FULL, (d) => { d.vulnerabilities = {}; recount(d); });
  const r = evaluate({ ...base(), full: clean });
  assert.equal(r.ok, true);
  assert.equal(r.status, 'PASS');
  assert.match(r.note, /remove SEC-EX-001/);
});
