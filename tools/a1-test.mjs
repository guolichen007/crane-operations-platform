// Isolated, anonymous PostgreSQL + TLS OIDC integration counterexamples.
import { spawnSync } from 'node:child_process';
import { mkdirSync, writeFileSync, chmodSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { randomBytes } from 'node:crypto';

const root = resolve('deploy/compose/runtime/a1-test');
mkdirSync(root, { recursive: true, mode: 0o700 }); chmodSync(root, 0o700);
const go = existsSync('tools/.cache/go/bin/go.exe') ? resolve('tools/.cache/go/bin/go.exe') : 'go';
const docker = process.platform === 'win32' ? 'C:/Program Files/Docker/Docker/resources/bin/docker.exe' : 'docker';
const environment = {
  ...process.env, GOCACHE: resolve('tools/.cache/gobuild'), GOMODCACHE: resolve('tools/.cache/gomod'), GOPATH: resolve('tools/.cache/gopath'),
  GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0',
};
const packages = [
  ['platform', './internal/platform', 'TestProductionRejectsDevelopmentTokenAtLoad'],
  ['identity', './internal/identity', 'TestExplicitSubjectBindingGrantsRevokeAndAudit'],
  ['webauth', './internal/webauth', 'TestOIDCStateNoncePKCEIssuerAudienceAndIdentityCounterexamples'],
  ['api', './services/cloud-api/internal/app', 'TestA1ProjectScopeFirstDirectObjectsAndViewerOwnership'],
];
function run(command, args, extra = {}, capture = false) {
  const result = spawnSync(command, args, { encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit', env: { ...environment, ...extra } });
  if (result.error || result.status !== 0) {
    if (capture) { process.stdout.write(result.stdout || ''); process.stderr.write(result.stderr || ''); }
    throw Error(`A1 integration command failed: ${command}`);
  }
  return result.stdout || '';
}
for (const [name, path] of packages) run(go, ['test', '-c', '-o', resolve(root, name + '.test'), path]);
const credentialFile = resolve(root, '.env');
writeFileSync(credentialFile, `A1_DB_PASSWORD=${randomBytes(32).toString('hex')}\n`, { mode: 0o600 }); chmodSync(credentialFile, 0o600);
const project = `crane-a1-test-${process.pid}`;
const compose = ['compose', '--project-name', project, '--env-file', credentialFile, '-f', 'tests/integration/a1/compose.yml'];
const first = { A1_TEST_BINARY: resolve(root, 'platform.test') };
const evidence = [];
try {
  run(docker, [...compose, 'config', '--quiet'], first);
  run(docker, [...compose, 'up', '-d', '--wait', 'postgres'], first);
  for (const [name, , required] of packages) {
    const output = run(docker, [...compose, 'run', '--rm', '--no-deps', 'test-runner', '-test.v', '-test.count=1', '-test.timeout=120s'], { A1_TEST_BINARY: resolve(root, name + '.test') }, true);
    process.stdout.write(output);
    if (!output.includes('--- PASS: ' + required)) throw Error('A1 required counterexample was skipped: ' + name);
    evidence.push({ package: name, status: 'PASS' });
  }
  const directory = resolve('local-private/validation/phase1-a1'); mkdirSync(directory, { recursive: true, mode: 0o700 });
  writeFileSync(resolve(directory, 'core-integration.json'), JSON.stringify({ status: 'PASS', packages: evidence }, null, 2), { mode: 0o600 });
  console.log('A1_CORE_INTEGRATION=PASS packages=' + evidence.length);
} finally {
  run(docker, [...compose, 'down', '--volumes', '--remove-orphans'], first);
}
