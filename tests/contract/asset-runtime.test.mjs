import assert from 'node:assert/strict';
import { test } from 'node:test';
import { chmodSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { assetServices, assetUpArgs, assertAssetServiceScope, prepareAssetInventory, secureRuntime } from '../../tools/asset-runtime.mjs';

test('asset-up names only the five permitted services on Windows and Unix', () => {
  for (const build of [true, false]) {
    assert.deepEqual(assetUpArgs(build).slice(3), ['postgres', 'mosquitto', 'cloud-api', 'web-console', 'device-simulator']);
  }
  const config = { services: Object.fromEntries(assetServices.map(name => [name, {}])) };
  config.services['cloud-api'].depends_on = { postgres: {}, mosquitto: {} };
  assert.deepEqual(assertAssetServiceScope(config), [...assetServices].sort());
  for (const forbidden of ['remote-gateway', 'pointcloud-bridge']) {
    config.services['cloud-api'].depends_on[forbidden] = {};
    assert.throws(() => assertAssetServiceScope(config), /exceeds/);
    delete config.services['cloud-api'].depends_on[forbidden];
  }
});

test('private artifacts are created privately and existing permissive modes are corrected before regeneration', t => {
  const root = mkdtempSync(join(tmpdir(), 'crane-private-runtime-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const directory = join(root, 'runtime'), input = join(root, 'anonymous.json');
  const sample = '{"anonymous":true}\n';
  writeFileSync(input, sample, { mode: 0o644 });
  const mode = path => statSync(path).mode & 0o777;
  const check = () => {
    if (process.platform === 'win32') return; // NTFS does not implement POSIX owner/group modes.
    assert.equal(mode(directory), 0o700);
    for (const name of ['.env', 'inventory.json', 'fleet.json', 'acl']) assert.equal(mode(join(directory, name)), 0o600, name);
  };
  prepareAssetInventory(input, directory);
  assert.equal(readFileSync(join(directory, 'inventory.json'), 'utf8'), sample);
  if (process.platform !== 'win32') {
    assert.equal(mode(directory), 0o700);
    assert.equal(mode(join(directory, 'inventory.json')), 0o600);
  }
  for (const name of ['.env', 'fleet.json', 'acl']) writeFileSync(join(directory, name), 'anonymous fixture', { mode: 0o644 });
  chmodSync(directory, 0o755);
  chmodSync(join(directory, 'inventory.json'), 0o644);
  secureRuntime(directory);
  check(); // before generation, including existing .env, Fleet and ACL.
  prepareAssetInventory(input, directory);
  check();
});

test('Git and Docker keep private inventory and runtime outside their contexts', () => {
  for (const path of ['.gitignore', '.dockerignore']) {
    const patterns = readFileSync(path, 'utf8').split(/\r?\n/).map(line => line.trim().replace(/\/$/, ''));
    assert.ok(patterns.includes('local-private'), path);
    assert.ok(patterns.includes('deploy/compose/runtime'), path);
  }
});
