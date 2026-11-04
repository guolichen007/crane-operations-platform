import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { resolve, join } from 'node:path';
import { createInterface } from 'node:readline';
import { once } from 'node:events';

const api = resolve('services/project-intake-api');
const dist = resolve('apps/project-intake-web/dist');
const go = process.env.BAOBEI_GO || 'go';
if (!existsSync(join(dist, 'index.html')) || !process.env.BAOBEI_TEST_DATABASE_URL)
  throw new Error('Build the Web and provide the disposable loopback PostgreSQL DSN first.');

const tmp = mkdtempSync(join(tmpdir(), 'baobei-auth-browser-'));
const binary = join(tmp, process.platform === 'win32' ? 'fixture.exe' : 'fixture');
try {
  const build = spawnSync(go, ['test', '-c', '-o', binary, '.'], { cwd: api, stdio: 'inherit' });
  if (build.status !== 0) throw build.error || new Error('Auth fixture build failed');
  const fixture = spawn(binary, ['-test.run=^TestAuthBrowserFixture$', '-test.v'], {
    cwd: api,
    env: { ...process.env, BAOBEI_AUTH_FIXTURE_DIST: dist },
    stdio: ['pipe', 'pipe', 'inherit'],
  });
  const exited = once(fixture, 'exit');
  const lines = createInterface({ input: fixture.stdout });
  try {
    const url = await new Promise((res, reject) => {
      const timer = setTimeout(() => reject(new Error('Auth browser fixture startup timed out')), 25000);
      fixture.once('error', err => { clearTimeout(timer); reject(err); });
      lines.once('close', () => { clearTimeout(timer); reject(new Error('Auth browser fixture stopped')); });
      lines.on('line', line => {
        if (/^AUTH_FIXTURE_URL=https:\/\/127\.0\.0\.1:\d+$/.test(line)) {
          clearTimeout(timer);
          res(line.slice('AUTH_FIXTURE_URL='.length));
        } else {
          console.log(line);
        }
      });
    });
    const playwright = spawn(process.execPath, [
      'apps/project-intake-web/node_modules/playwright/cli.js', 'test', '--config',
      'tests/baobei/auth-playwright.config.ts',
    ], { stdio: 'inherit', env: { ...process.env, BAOBEI_AUTH_FIXTURE_URL: url } });
    const [status] = await once(playwright, 'exit');
    if (status !== 0) throw new Error('Production auth browser regression failed');
  } finally {
    fixture.stdin.end();
    const timer = setTimeout(() => fixture.kill(), 10000);
    try {
      const [status] = await exited;
      if (status !== 0) throw new Error('Auth browser fixture cleanup failed');
    } finally {
      clearTimeout(timer);
    }
  }
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
