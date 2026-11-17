// R7 F-10: one authoritative asset type list, identical in the Go service and the web console,
// disjoint from the Equipment component type registry.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const goList = (src, name) => [...(new RegExp(`var ${name} = \\[\\]string\\{([^}]*)\\}`).exec(src)?.[1] ?? '').matchAll(/"([A-Z0-9_]+)"/g)].map((m) => m[1]);

test('asset types: Go AssetTypes equals the web console ASSET_TYPES', () => {
  const go = goList(readFileSync('internal/asset/asset.go', 'utf8'), 'AssetTypes');
  const ts = [...(/ASSET_TYPES = \[([^\]]*)\]/.exec(readFileSync('apps/web-console/src/cloud/asset-types.ts', 'utf8'))?.[1] ?? '').matchAll(/'([A-Z0-9_]+)'/g)].map((m) => m[1]);
  assert.ok(go.length > 0);
  assert.deepEqual(ts, go);
});

test('asset types never overlap the Equipment type registry', () => {
  const go = goList(readFileSync('internal/asset/asset.go', 'utf8'), 'AssetTypes');
  const equipment = [...readFileSync('internal/equipment/types.go', 'utf8').matchAll(/\{"([A-Z0-9_]+)", "/g)].map((m) => m[1]);
  assert.equal(equipment.length, 43);
  for (const t of go) assert.ok(!equipment.includes(t), `${t} is also an Equipment type`);
});

test('the asset create form uses the shared list (no second hard-coded list)', () => {
  const center = readFileSync('apps/web-console/src/views/AssetCenter.vue', 'utf8');
  assert.match(center, /const typeOptions = ASSET_TYPES;/);
  assert.match(readFileSync('apps/web-console/src/views/AssetDetail.vue', 'utf8'), /assetTypeOptions\(/);
});
