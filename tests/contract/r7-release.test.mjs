// R7 release artifacts: a separate, verifiable simulator image; the two-image candidate manifest
// is unchanged; OCI transport keeps the registry digest.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { IMAGE_SOURCES, SIMULATOR_SOURCE } from '../../tools/release-lib.mjs';
import { imagesOf, checkManifest, sha256 } from '../../tools/oci-transport.mjs';

test('the release / dev-candidate image list is still exactly cloud-api + web-console', () => {
  assert.deepEqual(IMAGE_SOURCES.map(([n]) => n), ['cloud-api', 'web-console']);
  assert.match(readFileSync('deploy/scripts/release-check.mjs', 'utf8'), /manifest\.images\.length !== 2/);
});

test('simulator is a separate artifact from the reviewed backend Dockerfile with a fixed target', () => {
  assert.deepEqual(SIMULATOR_SOURCE, ['device-simulator', 'deploy/Dockerfile.backend', ['TARGET=simulators/device-simulator']]);
  const dc = readFileSync('tools/dev-candidate.mjs', 'utf8');
  assert.match(dc, /simulator-manifest\.json/);
  assert.match(dc, /sources: \[SIMULATOR_SOURCE\]/);
  assert.ok(dc.indexOf("'dev-manifest.json'") < dc.indexOf('SIMULATOR_SOURCE]'), 'dev-manifest is written before and without the simulator');
  assert.match(readFileSync('.github/workflows/dev-candidate.yml', 'utf8'), /simulator-manifest\.json/);
  assert.match(readFileSync('deploy/compose/docker-compose.prod.simulation.yml', 'utf8'), /profiles: \[simulation\]/);
});

test('oci transport: manifest digest is verified from the raw bytes; wrong platform or digest refused', () => {
  const index = JSON.stringify({ schemaVersion: 2, mediaType: 'application/vnd.oci.image.index.v1+json', manifests: [
    { digest: 'sha256:' + 'a'.repeat(64), platform: { os: 'linux', architecture: 'amd64' } },
    { digest: 'sha256:' + 'b'.repeat(64), platform: { os: 'unknown', architecture: 'unknown' } }] });
  const d = sha256(index);
  assert.deepEqual(checkManifest(index, d).platforms, ['linux/amd64']);
  assert.throws(() => checkManifest(index, 'sha256:' + '0'.repeat(64)), /do not match/);
  assert.throws(() => checkManifest(index, d, { os: 'linux', architecture: 'arm64' }), /no linux\/arm64/);
  const sha = 'c'.repeat(40);
  const ok = { schema_version: 1, source_sha: sha, images: [{ source_sha: sha, image_name: 'ghcr.io/guolichen007/crane-operations-platform/dev/device-simulator', image_digest: d }] };
  assert.equal(imagesOf(ok)[0].name, 'device-simulator');
  assert.throws(() => imagesOf({ ...ok, images: [{ ...ok.images[0], image_name: 'docker.io/evil/x' }] }), /Invalid image/);
  assert.throws(() => imagesOf({ ...ok, images: [{ ...ok.images[0], image_digest: 'latest' }] }), /Invalid image/);
  const src = readFileSync('tools/oci-transport.mjs', 'utf8');
  assert.match(src, /--preserve-digests/);
  assert.doesNotMatch(src, /docker', \['save/);
});
