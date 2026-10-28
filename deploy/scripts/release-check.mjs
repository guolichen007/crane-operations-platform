import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { dirname, resolve } from 'node:path';
import { checkProductionDisks } from './disk-check.mjs';
import { assertExactCheckout, repositoryRoot } from './source-check.mjs';

export function verifyRelease(path, root = repositoryRoot) {
  path = resolve(path);
  const manifest = JSON.parse(readFileSync(path, 'utf8'));
  if (manifest.schema_version !== 1 || !/^[a-f0-9]{40}$/.test(manifest.source_sha || '') || !Array.isArray(manifest.images) || manifest.images.length !== 2) throw Error('Invalid release manifest');
  assertExactCheckout(manifest.source_sha, root);
  if (new Set(manifest.images.map(item => item.image_name)).size !== 2) throw Error('Cloud and Web images must both be present');
  for (const item of manifest.images) {
    if (item.source_sha !== manifest.source_sha || !/^sha256:[a-f0-9]{64}$/.test(item.image_digest) || !/^ghcr.io\/guolichen007\/crane-operations-platform\/(cloud-api|web-console)$/.test(item.image_name) || !/^(cloud-api|web-console)\.cdx\.json$/.test(item.sbom_file)) throw Error('Release image/source mismatch');
    const actual = createHash('sha256').update(readFileSync(resolve(dirname(path), item.sbom_file))).digest('hex');
    if (actual !== item.sbom_sha256) throw Error('SBOM checksum mismatch');
  }
  return manifest;
}
if (import.meta.main) {
  try {
    if (process.env.CRANE_DISK_PATH) throw Error('CRANE_DISK_PATH is retired; DockerRootDir must be discovered');
    const manifest = verifyRelease(process.argv[2] || 'release-manifest.json');
    checkProductionDisks({ backupRoot: process.argv[3] || process.env.CRANE_BACKUP_ROOT, expectedDockerRoot: process.env.CRANE_DOCKER_ROOT_DIR });
    for (const item of manifest.images) console.log(`${item.image_name.endsWith('/cloud-api') ? 'CRANE_CLOUD_IMAGE' : 'CRANE_WEB_IMAGE'}=${item.image_name}@${item.image_digest}`);
    console.log('RELEASE_PREFLIGHT=PASS source_sha=' + manifest.source_sha);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
