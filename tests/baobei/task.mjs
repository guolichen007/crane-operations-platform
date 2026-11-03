import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomBytes } from 'node:crypto';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
process.chdir(root);
const api = resolve('services/project-intake-api'),
  web = resolve('apps/project-intake-web');
const go =
  process.env.BAOBEI_GO ||
  (existsSync('tools/.cache/go/bin/go.exe') ? resolve('tools/.cache/go/bin/go.exe') : 'go');
const env = { ...process.env };
function run(cmd, args, cwd = root) {
  const r = spawnSync(cmd, args, { cwd, env, stdio: 'inherit', shell: false });
  if (r.error) throw r.error;
  if (r.status !== 0) throw Error(`${cmd} exited ${r.status}`);
}
const npmCli = resolve(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js');
const npm = (...args) =>
  process.platform === 'win32'
    ? run(process.execPath, [npmCli, ...args], web)
    : run('npm', args, web);
const composeArgs = [
  'compose',
  '--env-file',
  'local-private/baobei/compose.env',
  '-f',
  'deploy/baobei/compose.yaml',
  ...(process.env.BAOBEI_OFFLINE === '1' ? ['-f', 'deploy/baobei/compose.offline.yaml'] : []),
];
function compose(...args) {
  mkdirSync('local-private/baobei', { recursive: true });
  if (!existsSync('local-private/baobei/compose.env'))
    writeFileSync(
      'local-private/baobei/compose.env',
      `BAOBEI_DB_PASSWORD=${randomBytes(24).toString('hex')}\n`,
      { mode: 0o600 }
    );
  run('docker', [
    ...(env.BAOBEI_DOCKER_CONTEXT ? ['--context', env.BAOBEI_DOCKER_CONTEXT] : []),
    ...composeArgs,
    ...args,
  ]);
}
const tasks = {
  'production-check'() {
    run(process.execPath, ['tests/baobei/production.mjs']);
  },
  backup() {
    run(process.execPath, ['tests/baobei/backup.mjs', 'backup']);
  },
  'restore-test'() {
    env.BAOBEI_BACKUP_VERIFY = '1';
    run(process.execPath, ['tests/baobei/backup.mjs', 'backup']);
  },
  release() {
    run(process.execPath, ['tests/baobei/release.mjs']);
  },
  race() {
    run(go, ['test', '-race', '-count=1', './...'], api);
  },
  contract: () => run(process.execPath, ['tests/baobei/contract.mjs']),
  lint() {
    npm('run', 'typecheck');
    npm('run', 'lint', '--', '--max-warnings=0');
    run(go, ['vet', './...'], api);
    const r = spawnSync(go, ['fmt', './...'], { cwd: api, env, encoding: 'utf8' });
    if (r.status || r.stdout.trim()) throw Error('Go fmt drift');
  },
  test() {
    run(go, ['test', './...'], api);
    npm('test');
  },
  build() {
    mkdirSync('local-private', { recursive: true });
    run(
      go,
      [
        'build',
        '-o',
        resolve('local-private/baobei-api' + (process.platform === 'win32' ? '.exe' : '')),
        '.',
      ],
      api
    );
    npm('run', 'build');
  },
  integration() {
    if (!env.BAOBEI_TEST_DATABASE_URL) throw Error('NOT_RUN: BAOBEI_TEST_DATABASE_URL is required');
    run(go, ['test', '-count=1', '-run', 'TestDatabase', '-v', './...'], api);
  },
  e2e() {
    npm('exec', '--', 'playwright', 'test', '--config', '../../tests/baobei/playwright.config.ts');
  },
  security() {
    npm('audit', '--audit-level=high');
    run(process.execPath, ['tests/baobei/contract.mjs']);
    env.BAOBEI_GOVULNCHECK
      ? run(env.BAOBEI_GOVULNCHECK, ['./...'], api)
      : run(go, ['run', 'golang.org/x/vuln/cmd/govulncheck@v1.9.0', './...'], api);
  },
  compose() {
    compose('config', '--quiet');
    run(process.execPath, ['tests/baobei/production.mjs']);
  },
  up() {
    compose('up', '-d', '--build', '--wait', '--wait-timeout', '180');
  },
  down() {
    compose('down');
  },
  'docker-build'() {
    compose('build');
  },
};
const task = process.argv[2];
try {
  if (!tasks[task]) throw Error('Usage: baobei ' + Object.keys(tasks).join('|'));
  tasks[task]();
} catch (e) {
  console.error(e.message);
  process.exitCode = 1;
}
