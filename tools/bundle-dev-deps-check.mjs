// Production browser bundle check (CRANE-SEC-001): builds apps/web-console exactly like
// deploy/Dockerfile.web-production (VITE_DATA_MODE=api, VITE_AUTH_MODE=oidc) and records every module
// that ends up in a chunk. Development tooling (picomatch and its propagation) must not be among them.
//
//   node tools/bundle-dev-deps-check.mjs [--report-dir DIR]
import { build } from 'vite';
import { readFileSync, writeFileSync, mkdirSync, rmSync, mkdtempSync, existsSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { bundleProblems, EXCEPTION_FILE } from './npm-audit-gate.mjs';

const flag = (n) => { const i = process.argv.indexOf(n); return i > 0 ? process.argv[i + 1] : undefined; };
const dir = resolve(flag('--report-dir') ?? 'test-results-security');
mkdirSync(dir, { recursive: true });
// Without an exception record, still check the known development tooling packages.
const exception = existsSync(EXCEPTION_FILE) ? JSON.parse(readFileSync(EXCEPTION_FILE, 'utf8')) : { forbiddenInBrowserBundle: ['picomatch', 'fdir', 'tinyglobby', 'vitest', '@vue/language-core', 'vue-tsc'] };
const modules = new Set();
const out = mkdtempSync(join(tmpdir(), 'crane-bundle-'));
process.env.VITE_DATA_MODE = 'api';
process.env.VITE_AUTH_MODE = 'oidc';
try {
  await build({
    root: resolve('apps/web-console'),
    configFile: resolve('apps/web-console/vite.config.ts'),
    logLevel: 'warn',
    build: { outDir: out, emptyOutDir: true, sourcemap: false },
    plugins: [{
      name: 'crane-collect-modules',
      generateBundle(_, bundle) {
        for (const chunk of Object.values(bundle)) if (chunk.type === 'chunk') for (const id of Object.keys(chunk.modules)) modules.add(id.replaceAll('\\', '/'));
      },
    }],
  });
} finally { rmSync(out, { recursive: true, force: true }); }
const ids = [...modules].sort();
writeFileSync(join(dir, 'browser-bundle-modules.txt'), ids.join('\n') + '\n');
const problems = bundleProblems(ids, exception.forbiddenInBrowserBundle);
const nodeModules = new Set(ids.filter((id) => id.includes('/node_modules/')).map((id) => id.split('/node_modules/').pop().split('/').slice(0, id.split('/node_modules/').pop().startsWith('@') ? 2 : 1).join('/')));
console.log(`BROWSER_BUNDLE modules=${ids.length} packages=${[...nodeModules].sort().join(',')}`);
if (ids.length === 0) problems.push('no modules collected (build failed?)');
if (problems.length) {
  for (const p of problems) console.error('BUNDLE_PROBLEM ' + p);
  console.error('BROWSER_BUNDLE_DEV_DEPS=FAIL');
  process.exitCode = 1;
} else console.log(`BROWSER_BUNDLE_DEV_DEPS=NONE (checked: ${exception.forbiddenInBrowserBundle.join(', ')})`);
