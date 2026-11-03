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
console.log(`OpenAPI, transfer schema/hash and ${actions} action bindings PASS`);

assert.equal(
  payloadHash(fixture),
  payloadHash(Object.fromEntries(Object.entries(fixture).reverse()))
);
