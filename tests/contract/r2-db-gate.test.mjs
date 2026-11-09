// Gate logic for the isolated PostgreSQL batches (tools/r2-db-gate.mjs) and the
// H2-1A required-test list in tools/r2-b2-test.mjs. Proves the gate is not a
// formality: a skipped, missing, failed, prefix-only or unexpected test fails it.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { gateProblems, passed, runPattern } from '../../tools/r2-db-gate.mjs';

const REQUIRED = [
  'TestInventoryScopeStatusLifecycleAndDedup',
  'TestInventoryProjectCountsMatchAssetList',
  'TestInventoryScopeKeepsRetiredAssetHistory',
];
const ok = (names) => names.map((n) => `=== RUN   ${n}\n--- PASS: ${n} (0.11s)`).join('\n') + '\nPASS\n';

test('all required tests PASS on the real database → gate passes', () => {
  assert.deepEqual(gateProblems(ok(REQUIRED), REQUIRED, { exact: true }), []);
});

test('one required test SKIPped (no database URL) → gate fails', () => {
  const out = ok(REQUIRED.slice(0, 2)) + `=== RUN   ${REQUIRED[2]}\n    scope_test.go:12: real PostgreSQL assertions run in Compose with A0_TEST_DATABASE_URL\n--- SKIP: ${REQUIRED[2]} (0.00s)\nPASS\n`;
  const problems = gateProblems(out, REQUIRED, { exact: true });
  assert.ok(problems.includes('skipped test present'));
  assert.ok(problems.includes(`required test not PASSed: ${REQUIRED[2]}`));
});

test('one required test missing (renamed / filtered out) → gate fails', () => {
  assert.deepEqual(gateProblems(ok(REQUIRED.slice(0, 2)), REQUIRED, { exact: true }), [`required test not PASSed: ${REQUIRED[2]}`]);
});

test('a FAIL anywhere, or no final PASS line → gate fails', () => {
  assert.ok(gateProblems(ok(REQUIRED) + '--- FAIL: TestOther (0.01s)\n', REQUIRED).includes('test binary did not PASS'));
  assert.ok(gateProblems(ok(REQUIRED).replace(/\nPASS\n$/, '\nFAIL\n'), REQUIRED).includes('test binary did not PASS'));
});

test('a prefix-only match does not count (TestX vs TestXExtra)', () => {
  assert.equal(passed('--- PASS: TestInventoryScopeStatusLifecycleAndDedupExtra (0.1s)\n', 'TestInventoryScopeStatusLifecycleAndDedup'), false);
  assert.equal(passed('--- PASS: TestInventoryScopeStatusLifecycleAndDedup (0.1s)\n', 'TestInventoryScopeStatusLifecycleAndDedup'), true);
});

test('exact mode rejects unexpected extra top-level tests', () => {
  assert.deepEqual(gateProblems(ok([...REQUIRED, 'TestSomethingElse']), REQUIRED, { exact: true }), ['unexpected tests ran: TestSomethingElse']);
});

test('runPattern anchors each name', () => {
  assert.equal(runPattern(['TestA', 'TestB']), '^(TestA|TestB)$');
});

test('r2-b2-test.mjs gates the four H2-1A real-PostgreSQL tests and gives inventory tests a database URL', () => {
  const runner = readFileSync('tools/r2-b2-test.mjs', 'utf8');
  for (const name of [...REQUIRED, 'TestH21AInventoryScopeAuthorizationAndRetiredHistory', 'TestOperationsSummaryHTTP',
    'TestSummaryIdentityRuntimeAndAlarms', 'TestSummaryAuthorizationScopes', 'TestSummaryGeographyCountryProvincePrefectureCounty',
    'TestSummaryFutureSkewBoundariesAndConfig', 'TestSummaryEmptyDatabaseIsLegalZero', 'TestSummaryMoreThan100RecordsAndReviewAssets',
    'TestSummaryQueryFailureIsUnavailableNotPartial', 'TestSummaryConcurrentWritersSeeOneSnapshot',
    'TestSummaryLeavesNoSessionSettingOnPooledConnection', 'TestSnapshotInvariantDetectsTornReader', 'TestSummaryRuntimeInvariants',
    'TestSeedVerifyReseedCleanup', 'TestVerifyDetectsDrift', 'TestVerifyDetectsGrantLeak', 'TestGuardRefusesNonPreview'])
    assert.ok(runner.includes(`'${name}'`), `missing required test ${name}`);
  assert.match(runner, /\['h21a-inventory', '\.\/internal\/inventory'/);
  assert.match(runner, /\['h21a-cloudapi', '\.\/services\/cloud-api\/internal\/app'/);
  assert.match(runner, /\['h21c-operations', '\.\/internal\/operations'/);
  assert.match(runner, /\['h3-uatdata', '\.\/tools\/uatdata'/);
  assert.match(runner, /runTargetGate\(name, required\)/);
  assert.match(runner, /gateProblems\(output, required, \{ exact: true \}\)/);
  const compose = readFileSync('tests/integration/a1/compose.yml', 'utf8');
  assert.match(compose, /A0_TEST_DATABASE_URL: postgres:\/\/a1test:/);
  assert.match(compose, /A1_TEST_DATABASE_URL: postgres:\/\/a1test:/);
  const ci = readFileSync('.github/workflows/ci.yml', 'utf8');
  assert.match(ci, /tests\/contract\/r2-db-gate\.test\.mjs/);
  assert.match(ci, /run: node tools\/r2-b2-test\.mjs/);
});
