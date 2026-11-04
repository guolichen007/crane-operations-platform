// Isolated anonymous PostgreSQL B2/B3 DB counterexamples.
import { spawnSync } from 'node:child_process';
import { mkdirSync, writeFileSync, chmodSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { randomBytes } from 'node:crypto';

const root = resolve('deploy/compose/runtime/r2-b2-test');
mkdirSync(root, { recursive: true, mode: 0o700 }); chmodSync(root, 0o700);
const go = existsSync('tools/.cache/go/bin/go.exe') ? resolve('tools/.cache/go/bin/go.exe') : 'go';
const docker = process.platform === 'win32' ? 'C:/Program Files/Docker/Docker/resources/bin/docker.exe' : 'docker';
const environment = {
  ...process.env, GOCACHE: resolve('tools/.cache/gobuild'), GOMODCACHE: resolve('tools/.cache/gomod'), GOPATH: resolve('tools/.cache/gopath'),
  GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0',
};
// [name, package path, required PASS tests] — a required test that would otherwise
// Skip on a missing DATABASE_URL; skipping is a hard failure, never a silent PASS.
const b2Packages = [
  ['portfolio', './internal/portfolio', ['TestIntakeStateMachineAndApprove']],
  ['scheduling', './internal/scheduling', [
    'TestConcurrentExclusiveApprovalSingleWinner',
    'TestCrossPortfolioReservationRejected',
    'TestDependencyCycleDetection',
    'TestConflictReconcileIdempotentAndReschedule',
    'TestCalendarFullIntervalEngine',
    'TestCalendarTimezoneDST',
  ]],
  ['identity', './internal/identity', ['TestPortfolioGrantIsolation']],
];
const b3Packages = [
  ['asset', './internal/asset', [
    'TestCreateAssetIdempotencyRetry',
    'TestCreateAssetConcurrentIdempotency',
    'TestUpdateAssetStaleIfMatch',
    'TestAssetRetireWithActiveBindingRejected',
    'TestAssetMoveHistory',
    'TestManufacturerCrossProjectRejected',
    'TestCatalogGlobalReadAndCrossProjectReject',
    'TestEquipmentActiveBindingRemoveRejected',
    'TestRevisionAndAuditWritten',
  ]],
  ['migrations', './deploy/postgres/migrations', [
    'TestMigrationUpFromEmptyAppliesSpec',
    'TestMigrationDownRefusedKeepsVersionConsistent',
    'TestMigrationUpFromOldDatabase',
  ]],
];
function run(command, args, extra = {}, capture = false) {
  const result = spawnSync(command, args, { encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit', env: { ...environment, ...extra } });
  if (result.error || result.status !== 0) {
    if (capture) { process.stdout.write(result.stdout || ''); process.stderr.write(result.stderr || ''); }
    throw Error(`R2 DB test command failed: ${command}`);
  }
  return result.stdout || '';
}
// runGate executes one authoritative package and enforces two invariants: every
// required counterexample PASSed, and the package produced zero SKIP lines. A
// skipped test — required or not — in an authoritative package is a FAIL.
function runGate(batch, name) {
  const output = run(docker, [...compose, 'run', '--rm', '--no-deps', 'test-runner', '-test.v', '-test.count=1', '-test.timeout=120s'], { A1_TEST_BINARY: resolve(root, name + '.test') }, true);
  process.stdout.write(output);
  if (output.includes('--- SKIP:')) throw Error(`R2 ${batch} authoritative package has a skipped test: ${name}`);
  for (const required of requiredByName(name, batch)) {
    if (!output.includes('--- PASS: ' + required)) throw Error(`R2 ${batch} required counterexample was skipped or failed: ${name} :: ${required}`);
  }
}
function requiredByName(name, batch) {
  return (batch === 'B2' ? b2Packages : b3Packages).find(([n]) => n === name)[2];
}
for (const [name, path] of [...b2Packages, ...b3Packages]) run(go, ['test', '-c', '-o', resolve(root, name + '.test'), path]);
const credentialFile = resolve(root, '.env');
writeFileSync(credentialFile, `A1_DB_PASSWORD=${randomBytes(32).toString('hex')}\n`, { mode: 0o600 }); chmodSync(credentialFile, 0o600);
const project = `crane-r2-b2-test-${process.pid}`;
const compose = ['compose', '--project-name', project, '--env-file', credentialFile, '-f', 'tests/integration/a1/compose.yml'];
const first = { A1_TEST_BINARY: resolve(root, 'portfolio.test') };
try {
  run(docker, [...compose, 'config', '--quiet'], first);
  run(docker, [...compose, 'up', '-d', '--wait', 'postgres'], first);
  for (const [name] of b2Packages) runGate('B2', name);
  console.log('R2_B2_DB_TEST=PASS');
  for (const [name] of b3Packages) runGate('B3', name);
  console.log('R2_B3_DB_TEST=PASS');
} finally {
  run(docker, [...compose, 'down', '--volumes', '--remove-orphans'], first);
}
