import './closure.mjs';
import { createRequire } from 'node:module';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import assert from 'node:assert/strict';
import { validateTransfer, payloadHash } from '../../contracts/project-intake/validator.mjs';
const require = createRequire(
  new URL('../../apps/project-intake-web/package.json', import.meta.url)
);
const parser = require('@apidevtools/swagger-parser');
const spec = await parser.validate('contracts/project-intake/openapi.yaml');
assert.equal(spec.info.title, 'Project Intake API');
const fixture = JSON.parse(readFileSync('contracts/project-intake/transfer.fixture.json'));
assert.equal(validateTransfer(fixture).connected, false);
const invalid = structuredClone(fixture);
invalid.plannedEquipment[0].kind = 'EQUIPMENT';
invalid.payloadHash = payloadHash(invalid);
assert.throws(() => validateTransfer(invalid));
const tampered = structuredClone(fixture);
tampered.project.projectName += 'changed';
assert.throws(() => validateTransfer(tampered));
const matrix = readFileSync('docs/项目报备中心/交互动作矩阵.md', 'utf8');
let actions = 0;
for (const file of readdirSync('apps/project-intake-web/src').filter((f) => f.endsWith('.vue'))) {
  const source = readFileSync(resolve('apps/project-intake-web/src', file), 'utf8');
  assert(!/href=["']#/.test(source), file + ' dead href');
  assert(!/@click=["'](?:noop|\(\)\s*=>\s*\{\s*\})/.test(source), file + ' empty handler');
  assert(!/TODO action|console\.log/.test(source), file + ' placeholder');
  if (file === 'ActionButton.vue') assert(/type="button"/.test(source), 'button type missing');
  if (file !== 'ActionButton.vue')
    assert(!/<button\b/.test(source), file + ' bypasses ActionButton');
  for (const m of source.matchAll(/(?:^|\s)(?:data-)?action-id="([^"\n]+)"/g)) {
    assert(matrix.includes('| ' + m[1] + ' |'), file + ' undocumented ' + m[1]);
    actions++;
  }
}
assert(actions >= 20);
// Logout race guard: a click on auth.logout must be followed by waiting for /login,
// otherwise the next login can be redirected to / by a still-valid session.
for (const file of readdirSync('tests/baobei/e2e').filter((f) => f.endsWith('.ts'))) {
  const lines = readFileSync(resolve('tests/baobei/e2e', file), 'utf8').split('\n');
  lines.forEach((line, i) => {
    if (/action\((page|p|b)\s*,\s*'auth\.logout'\)\.click\(\)/.test(line))
      assert(lines.slice(i + 1, i + 4).some((l) => /toHaveURL\('\/login'\)/.test(l)), `${file}:${i + 1} logout without waiting for /login`);
  });
}
// Test isolation: only the B3 staff spec may use the accounts it irreversibly suspends.
for (const file of readdirSync('tests/baobei/e2e').filter((f) => f.endsWith('.ts') && f !== 'staff.spec.ts'))
  assert(!/['"`]synthetic-staff-/.test(readFileSync(resolve('tests/baobei/e2e', file), 'utf8')), file + ' uses a B3-suspended account');
console.log(`OpenAPI, transfer schema/hash and ${actions} action bindings PASS`);

assert.equal(
  payloadHash(fixture),
  payloadHash(Object.fromEntries(Object.entries(fixture).reverse()))
);

// Web runtime image: digest-pinned official nginx base, same-series security upgrade with a
// fail-closed minimum-version gate, and the Nginx syntax tests use the same pinned base.
{
  const df = readFileSync('deploy/baobei/web.Dockerfile', 'utf8');
  const froms = [...df.matchAll(/^FROM\s+(\S+)/gm)].map((m) => m[1]);
  const runtime = froms.at(-1);
  assert.match(runtime, /^nginx:[0-9.]+-alpine3\.24@sha256:[0-9a-f]{64}$/, 'web runtime base must be digest-pinned');
  const tail = df.slice(df.lastIndexOf('FROM '));
  assert.match(tail, /apk upgrade --no-cache/, 'web runtime must apply same-series apk security upgrades');
  assert.match(tail, /apk info -e 'libxslt>=1\.1\.43-r4' 'zlib>=1\.4-r0'/, 'web runtime fixed-package gate missing');
  for (const f of ['tests/baobei/http01.mjs', 'tests/baobei/preview.mjs', 'tests/baobei/production.mjs']) {
    const refs = [...readFileSync(f, 'utf8').matchAll(/'(nginx:[^']+)'/g)].map((m) => m[1]);
    assert(refs.length && refs.every((r) => r === runtime), f + ' must test Nginx with the pinned web runtime base');
  }
  console.log('Web runtime base pin and security upgrade gate PASS');
}
