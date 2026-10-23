import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

export const assetServices = Object.freeze(['postgres', 'mosquitto', 'cloud-api', 'web-console', 'device-simulator']);
export const runtimeDirectory = 'deploy/compose/runtime';

// Tighten existing paths before any private artifact is copied or regenerated.
export function secureRuntime(directory = runtimeDirectory) {
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  chmodSync(directory, 0o700);
  for (const name of ['.env', 'inventory.json', 'fleet.json', 'acl']) {
    const path = join(directory, name);
    if (existsSync(path)) chmodSync(path, 0o600);
  }
}

export function prepareAssetInventory(input, directory = runtimeDirectory) {
  secureRuntime(directory);
  writeFileSync(join(directory, 'inventory.json'), readFileSync(input), { mode: 0o600 });
}

export function assetUpArgs(build = true) {
  return ['up', '-d', build ? '--build' : '--no-build', ...assetServices];
}

// Input must be Docker Compose's resolved JSON, including all dependencies.
export function assertAssetServiceScope(config) {
  const resolved = new Set();
  function visit(name) {
    if (resolved.has(name)) return;
    if (!assetServices.includes(name)) throw Error('asset-up dependency exceeds the permitted service set');
    const service = config.services[name];
    if (!service) throw Error('asset-up service missing from resolved Compose configuration');
    resolved.add(name);
    for (const dependency of Object.keys(service.depends_on || {})) visit(dependency);
  }
  assetServices.forEach(visit);
  return [...resolved].sort();
}
