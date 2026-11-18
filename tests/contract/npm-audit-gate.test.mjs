// CRANE-SEC-001: npm audit gate with one approved, time-limited, development-only exception.
// Counter-proofs: the approved dev-only advisory passes (and is reported as an exception, not a fix);
// the same advisory in a production node, any other HIGH, any CRITICAL, an expired exception, a failed
// scan / malformed JSON and an unknown dependency chain all fail. The CI step uses the gate, not a
// skipped or non-fatal audit.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import YAML from 'yaml';
import { evaluate, bundleProblems, EXCEPTION_FILE } from '../../tools/npm-audit-gate.mjs';

const exception = JSON.parse(readFileSync(EXCEPTION_FILE, 'utf8'));
const NOW = new Date('2026-11-18T12:00:00Z');
const ADV = { source: 1247710, name: 'picomatch', dependency: 'picomatch', title: 'picomatch vulnerable to ReDoS', url: 'https://github.com/advisories/GHSA-fc5q-39xc-vjpf', severity: 'high', cwe: ['CWE-1333'], range: '<=4.0.7' };
const v = (name, severity, via, nodes = [`node_modules/${name}`], isDirect = false) => ({ name, severity, isDirect, via, effects: [], range: '*', nodes, fixAvailable: false });

/** The real shape of today's report (8 HIGH, all explained by the picomatch advisory; 6 moderate). */
function baseline() {
  const vulnerabilities = {
    picomatch: v('picomatch', 'high', [ADV]),
    fdir: v('fdir', 'high', ['picomatch']),
    tinyglobby: v('tinyglobby', 'high', ['fdir', 'picomatch']),
    vite: v('vite', 'high', ['fdir', 'picomatch', 'tinyglobby'], undefined, true),
    vitest: v('vitest', 'high', ['picomatch', 'tinyglobby', 'vite'], undefined, true),
    '@vue/language-core': v('@vue/language-core', 'high', ['picomatch']),
    'vue-tsc': v('vue-tsc', 'high', ['@vue/language-core'], undefined, true),
    '@typescript-eslint/typescript-estree': v('@typescript-eslint/typescript-estree', 'high', ['minimatch', 'tinyglobby']),
    minimatch: v('minimatch', 'moderate', [{ ...ADV, source: 1244623, name: 'minimatch', url: 'https://github.com/advisories/GHSA-g3hv-75wg-m249', severity: 'moderate', range: '>=7.0.0 <11.0.1' }], ['node_modules/@typescript-eslint/typescript-estree/node_modules/minimatch']),
    'typescript-eslint': v('typescript-eslint', 'moderate', ['@typescript-eslint/typescript-estree'], undefined, true),
  };
  const packages = { '': { name: 'crane-operations-platform' } };
  for (const x of Object.values(vulnerabilities)) for (const n of x.nodes) packages[n] = { version: x.name === 'picomatch' ? '4.0.7' : '1.0.0', dev: true };
  return {
    report: { auditReportVersion: 2, vulnerabilities, metadata: { vulnerabilities: { info: 0, low: 0, moderate: 2, high: 8, critical: 0, total: 10 } } },
    lock: { lockfileVersion: 3, packages },
  };
}

test('approved dev-only advisory passes as PASS_WITH_APPROVED_EXCEPTION (all 8 HIGH explained recursively)', () => {
  const { report, lock } = baseline();
  const r = evaluate(report, lock, exception, NOW);
  assert.deepEqual(r.problems, []);
  assert.equal(r.verdict, 'PASS_WITH_APPROVED_EXCEPTION');
  assert.equal(r.explained.length, 8);
  assert.equal(exception.status, 'APPROVED'); assert.match(exception.notAFix, /NOT fixed/);
  assert.equal(exception.expires, '2026-12-02T00:00:00Z');
  // the real lock really marks every picomatch install dev=true at the approved version
  const realLock = JSON.parse(readFileSync('package-lock.json', 'utf8'));
  const installs = Object.entries(realLock.packages).filter(([k]) => k.endsWith('node_modules/picomatch'));
  assert.ok(installs.length >= 1);
  for (const [k, e] of installs) { assert.equal(e.dev, true, k); assert.equal(e.version, '4.0.7', k); }
});

test('same advisory reaching a production dependency fails', () => {
  const { report, lock } = baseline();
  lock.packages['node_modules/picomatch'].dev = false;
  assert.match(evaluate(report, lock, exception, NOW).problems.join(';'), /picomatch.*NOT dev-only|not dev-only/);
  const b = baseline();
  b.lock.packages['node_modules/vite'].dev = undefined; // e.g. moved to dependencies
  assert.match(evaluate(b.report, b.lock, exception, NOW).problems.join(';'), /vite.*NOT dev-only/);
});

test('any other HIGH fails (other advisory, other package, unexplained metavulnerability)', () => {
  const a = baseline();
  a.report.vulnerabilities.lodash = v('lodash', 'high', [{ ...ADV, source: 1, name: 'lodash', url: 'https://github.com/advisories/GHSA-xxxx-yyyy-zzzz', range: '<4.17.21' }]);
  a.lock.packages['node_modules/lodash'] = { version: '4.17.20', dev: true };
  assert.match(evaluate(a.report, a.lock, exception, NOW).problems.join(';'), /unapproved advisory .*GHSA-xxxx-yyyy-zzzz/);
  // a second HIGH advisory on picomatch itself
  const b = baseline();
  b.report.vulnerabilities.picomatch.via.push({ ...ADV, source: 2, url: 'https://github.com/advisories/GHSA-aaaa-bbbb-cccc' });
  assert.match(evaluate(b.report, b.lock, exception, NOW).problems.join(';'), /GHSA-aaaa-bbbb-cccc/);
  // a HIGH whose severity comes only from a moderate advisory is not explained by the exception
  const c = baseline();
  c.report.vulnerabilities['@typescript-eslint/typescript-estree'].via = ['minimatch'];
  assert.match(evaluate(c.report, c.lock, exception, NOW).problems.join(';'), /not traced to the approved advisory/);
  // the approved GHSA on a different range / version is not covered
  const d = baseline();
  d.lock.packages['node_modules/picomatch'].version = '4.0.6';
  assert.match(evaluate(d.report, d.lock, exception, NOW).problems.join(';'), /4\.0\.6, exception covers 4\.0\.7 only/);
});

test('any CRITICAL fails', () => {
  const { report, lock } = baseline();
  report.vulnerabilities.picomatch.severity = 'critical';
  report.metadata.vulnerabilities.critical = 1;
  assert.match(evaluate(report, lock, exception, NOW).problems.join(';'), /CRITICAL/);
  const b = baseline();
  b.report.vulnerabilities.vite.via = ['fdir', { ...ADV, severity: 'critical', source: 3, url: 'https://github.com/advisories/GHSA-crit-crit-crit' }];
  b.report.metadata.vulnerabilities.critical = 1;
  assert.equal(evaluate(b.report, b.lock, exception, NOW).verdict, 'FAIL');
});

test('expired or unapproved exception fails', () => {
  const { report, lock } = baseline();
  assert.match(evaluate(report, lock, exception, new Date('2026-12-02T00:00:00Z')).problems.join(';'), /expired at 2026-12-02/);
  assert.match(evaluate(report, lock, { ...exception, status: 'PENDING' }, NOW).problems.join(';'), /no approved exception/);
  assert.match(evaluate(report, lock, { ...exception, expires: 'soon' }, NOW).problems.join(';'), /expiry is not a valid date/);
  assert.equal(evaluate(report, lock, null, NOW).problems.length, 8, 'no exception record: every HIGH fails');
});

test('scan failure, malformed JSON and an unknown dependency chain fail', () => {
  for (const bad of [null, {}, { auditReportVersion: 1, vulnerabilities: {} }, { auditReportVersion: 2, metadata: {} }, { error: { code: 'ENOAUDIT' } }])
    assert.equal(evaluate(bad, baseline().lock, exception, NOW).verdict, 'FAIL', JSON.stringify(bad));
  const a = baseline();
  a.report.error = { code: 'EAUDITNOLOCK' };
  assert.match(evaluate(a.report, a.lock, exception, NOW).problems.join(';'), /npm audit reported an error/);
  // a new package on the chain (dependency path changed)
  const b = baseline();
  b.report.vulnerabilities['new-tool'] = v('new-tool', 'high', ['picomatch']);
  b.lock.packages['node_modules/new-tool'] = { version: '1.0.0', dev: true };
  assert.match(evaluate(b.report, b.lock, exception, NOW).problems.join(';'), /new-tool: dependency chain not recorded/);
  // a via that is not in the report / a node missing from the lock
  const c = baseline();
  c.report.vulnerabilities.fdir.via = ['ghost'];
  assert.match(evaluate(c.report, c.lock, exception, NOW).problems.join(';'), /unknown via ghost/);
  const d = baseline();
  delete d.lock.packages['node_modules/tinyglobby'];
  assert.match(evaluate(d.report, d.lock, exception, NOW).problems.join(';'), /node_modules\/tinyglobby missing from package-lock/);
});

test('browser bundle check and CI wiring: gate replaces the plain audit; no skip / non-fatal audit; other scanners unchanged', () => {
  assert.deepEqual(bundleProblems(['/src/node_modules/vue/dist/x.js', '/src/apps/a.ts'], exception.forbiddenInBrowserBundle), []);
  assert.equal(bundleProblems(['/src/node_modules/picomatch/index.js'], exception.forbiddenInBrowserBundle).length, 1);
  const job = YAML.parse(readFileSync('.github/workflows/ci.yml', 'utf8')).jobs.security;
  const runs = job.steps.map((s) => s.run ?? '').join('\n');
  assert.match(runs, /npm ci\nnode tools\/npm-audit-gate\.mjs/);
  assert.match(runs, /node tools\/bundle-dev-deps-check\.mjs/);
  assert.doesNotMatch(runs, /\|\|\s*true|npm audit --audit-level=high/);
  assert.ok(job.steps.every((s) => s['continue-on-error'] === undefined), 'no non-fatal security step');
  assert.deepEqual(job.steps.filter((s) => s.if !== undefined).map((s) => s.uses?.split('@')[0]), ['actions/upload-artifact'], 'only the evidence upload is conditional (always)');
  for (const needle of ['gitleaks/v8@v8.30.1 git . --redact', 'govulncheck@v1.9.0 ./...', 'aquasec/trivy:0.74.0 fs --scanners vuln --severity HIGH,CRITICAL --exit-code 1', 'aquasec/trivy:0.74.0 image --input /input.tar --severity HIGH,CRITICAL --exit-code 1'])
    assert.ok(runs.includes(needle), needle);
});
