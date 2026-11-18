// SEC-EX-001 audit gate runner: runs npm audit itself (full and --omit=dev),
// keeps the raw JSON, evaluates with audit-gate-core.mjs and fails closed.
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { evaluate } from './audit-gate-core.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const web = resolve(root, 'apps/project-intake-web');
const out = resolve(root, 'local-private/baobei/security-audit');
const npmCli = resolve(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js');
function npm(args) {
  const r = process.platform === 'win32'
    ? spawnSync(process.execPath, [npmCli, ...args], { cwd: web, encoding: 'utf8', maxBuffer: 64 << 20 })
    : spawnSync('npm', args, { cwd: web, encoding: 'utf8', maxBuffer: 64 << 20 });
  if (r.error) throw r.error;
  // npm audit exits 1 when it finds vulnerabilities; the JSON is evaluated, not the exit code.
  if (r.status !== 0 && r.status !== 1) throw Error(`npm ${args.join(' ')} exited ${r.status}`);
  return r.stdout;
}
let result;
try {
  const full = npm(['audit', '--json']);
  const prod = npm(['audit', '--omit=dev', '--json']);
  const registry = npm(['view', 'picomatch', 'time', '--json']);
  mkdirSync(out, { recursive: true });
  writeFileSync(resolve(out, 'npm-audit-full.json'), full);
  writeFileSync(resolve(out, 'npm-audit-prod.json'), prod);
  writeFileSync(resolve(out, 'registry-picomatch-time.json'), registry);
  result = evaluate({
    full, prod, registry, now: new Date(),
    lock: readFileSync(resolve(web, 'package-lock.json'), 'utf8'),
    approval: readFileSync(resolve(root, 'tests/baobei/security/sec-ex-001.json'), 'utf8'),
  });
  writeFileSync(resolve(out, 'audit-gate-result.json'), JSON.stringify(result, null, 2));
} catch (error) {
  result = { ok: false, reasons: ['audit could not run: ' + error.message] };
}
if (!result.ok) {
  console.error('BAOBEI_NPM_AUDIT=FAIL');
  for (const r of result.reasons) console.error(' - ' + r);
  process.exit(1);
}
console.log('BAOBEI_NPM_AUDIT=' + result.status);
if (result.unfixed) {
  const u = result.unfixed;
  console.log(`UNFIXED_VULNERABILITY ${u.ghsa} ${u.cve} ${u.package} exception=${u.exception} deadline=${u.deadline} officialFix=${u.officialFix || 'none'}`);
  console.log('UNFIXED_NODES ' + u.nodes.join(' '));
  console.log('REPORTED_VIA_EXCEPTION ' + u.reported.join(' '));
}
if (result.note) console.log('NOTE ' + result.note);
