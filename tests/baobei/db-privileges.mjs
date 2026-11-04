import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';
import { setTimeout } from 'node:timers/promises';
import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';

const context = process.env.BAOBEI_DOCKER_CONTEXT;
function docker(args) {
  return spawnSync('docker', [...(context ? ['--context', context] : []), ...args], {
    encoding: 'utf8',
  });
}
function checked(args) {
  const result = docker(args);
  if (result.error) throw result.error;
  if (result.status !== 0) throw Error(result.stderr || 'Docker fixture command failed');
  return result.stdout.trim();
}

// Reuse the exact production PostgreSQL digest and initializer in a fresh fixture.
const compose = readFileSync('deploy/baobei/compose.production.yaml', 'utf8');
const [, image] = compose.match(/image: (postgres:18\.4-alpine@sha256:[0-9a-f]{64})/);
const container = checked([
  'run', '--rm', '-d', '-p', '127.0.0.1::5432',
  '--tmpfs', '/var/lib/postgresql',
  '-e', 'POSTGRES_USER=baobei_bootstrap', '-e', 'POSTGRES_DB=baobei',
  '-e', 'POSTGRES_HOST_AUTH_METHOD=trust',
  '-v', resolve('deploy/baobei/db/initialize.sql') + ':/docker-entrypoint-initdb.d/10-baobei-roles.sql:ro',
  image,
]);
try {
  let ready = false;
  for (let attempt = 0; attempt < 45; attempt++) {
    ready = docker(['exec', container, 'pg_isready', '-h', '127.0.0.1', '-U', 'baobei_bootstrap', '-d', 'baobei']).status === 0;
    if (ready) break;
    await setTimeout(1000);
  }
  assert(ready, 'Disposable production-role fixture did not start');
  const port = checked(['port', container, '5432/tcp']).match(/^127\.0\.0\.1:(\d+)$/)?.[1];
  assert(port, 'Fixture must bind a loopback port');
  const result = spawnSync(process.env.BAOBEI_GO || 'go', [
    'test', ...(process.platform === 'linux' ? ['-race'] : []),
    '-count=1', '-run', '^TestDatabaseProductionPrivileges$', '-v', './...',
  ], {
    cwd: 'services/project-intake-api',
    env: { ...process.env, BAOBEI_PRIVILEGE_TEST_PORT: port },
    stdio: 'inherit',
  });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, 'Production database privilege fixture failed');
} finally {
  checked(['stop', container]);
}
