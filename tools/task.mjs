import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve, dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomBytes } from 'node:crypto';
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
process.chdir(root);
const win = process.platform === 'win32';
const go = existsSync('tools/.cache/go/bin/go.exe') ? resolve('tools/.cache/go/bin/go.exe') : 'go';
const docker =
  win && existsSync('C:/Program Files/Docker/Docker/resources/bin/docker.exe')
    ? 'C:/Program Files/Docker/Docker/resources/bin/docker.exe'
    : 'docker';
const env = {
  ...process.env,
  GOCACHE: resolve('tools/.cache/gobuild'),
  GOMODCACHE: resolve('tools/.cache/gomod'),
  GOPATH: resolve('tools/.cache/gopath'),
  STATICCHECK_CACHE: resolve('tools/.cache/staticcheck'),
};
export function run(cmd, args, options = {}) {
  const r = spawnSync(cmd, args, { stdio: 'inherit', env, ...options });
  if (r.error) throw r.error;
  if (r.status !== 0) throw Error(`${cmd} exited ${r.status}`);
}
function npm(...args) {
  if (win)
    run(process.execPath, [
      join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'),
      ...args,
    ]);
  else run('npm', args);
}
const composeArgs = [
  'compose',
  '--env-file',
  'deploy/compose/runtime/.env',
  '-f',
  'deploy/compose/docker-compose.dev.yml',
];
function compose(...args) {
  run(docker, [...composeArgs, ...args]);
}
const tasks = {
  bootstrap() {
    mkdirSync('tools/.cache', { recursive: true });
    npm('ci');
    run(go, ['mod', 'download']);
    mkdirSync('deploy/compose/runtime', { recursive: true });
    if (!existsSync('deploy/compose/runtime/.env'))
      writeFileSync(
        'deploy/compose/runtime/.env',
        `POSTGRES_PASSWORD=${randomBytes(24).toString('hex')}\nDEV_API_TOKEN=${randomBytes(32).toString('hex')}\n`,
        { mode: 0o600 }
      );
    if (!existsSync('deploy/compose/runtime/certs/cloud/cloud-api.key'))
      run(go, ['run', './tools/certs']);
    else if (!existsSync('deploy/compose/runtime/certs/devices/TEST-001.key'))
      run(go, ['run', './tools/certs', '--fixture']);
  },
  'dev-up'() {
    if (win) {
      run(process.execPath, ['tools/build-images.mjs']);
      compose('up', '-d', '--no-build');
    } else compose('up', '-d', '--build');
  },
  'dev-down'() {
    compose('down', '--remove-orphans');
  },
  'dev-logs'() {
    compose('logs', '--tail', '100');
  },
  lint() {
    run(process.execPath, ['tools/codegen/contracts.mjs', '--check']);
    run(process.execPath, ['tools/codegen/openapi.mjs', '--check']);
    run(process.execPath, ['tools/diagnostics/repository.mjs']);
    run(go, ['vet', './...']);
    const format = spawnSync(go, ['fmt', './...'], { encoding: 'utf8', env });
    if (format.status !== 0 || format.stdout.trim())
      throw Error('Go formatting drift; run gofmt then commit');
    run(go, ['run', 'honnef.co/go/tools/cmd/staticcheck@v0.8.2', './...']);
    npm('run', 'typecheck');
    npm('run', 'lint');
  },
  test() {
    run(go, ['test', './...']);
    npm('test');
    run(process.execPath, ['--test', 'tests/contract/contracts.test.mjs']);
  },
  build() {
    run(go, ['build', './...']);
    npm('run', 'build');
  },
  'integration-test'() {
    run(process.execPath, ['tests/integration/run.mjs']);
  },
  security() {
    run(process.execPath, ['tools/diagnostics/repository.mjs']);
    npm('audit', '--audit-level=high');
    run(go, ['run', 'golang.org/x/vuln/cmd/govulncheck@v1.9.0', './...']);
  },
};
const task = process.argv[2];
if (!Object.hasOwn(tasks, task)) {
  console.error('Usage: node tools/task.mjs ' + Object.keys(tasks).join('|'));
  process.exitCode = 2;
} else {
  try {
    tasks[task]();
  } catch (e) {
    console.error(e.message);
    process.exitCode = 1;
  }
}
