// Shared, reviewed image build chain used by BOTH the production release (tools/release.mjs,
// main only) and the development candidate (tools/dev-candidate.mjs, dev/crane-* tags only).
// One implementation: build with SOURCE_SHA label → verify revision label → archive → Trivy
// HIGH/CRITICAL gate → CycloneDX SBOM → push full-SHA tag → read the registry digest.
// Callers decide only the registry namespace; nothing here deploys.
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

export const REPOSITORY = 'guolichen007/crane-operations-platform';
export const IMAGE_SOURCES = [['cloud-api', 'deploy/Dockerfile.backend'], ['web-console', 'deploy/Dockerfile.web-production']];
export const TRIVY = 'aquasec/trivy:0.74.0';

export function run(command, args, capture = false) {
  const result = spawnSync(command, args, { encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' });
  if (result.error || result.status) throw Error('Release command failed: ' + command + ' ' + (args[0] || ''));
  return result.stdout || '';
}

export function assertSource(sha, repository) {
  if (!/^[a-f0-9]{40}$/.test(sha || '')) throw Error('Release requires exact GITHUB_SHA');
  if ((repository || '').toLowerCase() !== REPOSITORY) throw Error('Unexpected release repository');
  if (run('git', ['rev-parse', 'HEAD'], true).trim() !== sha) throw Error('Checkout/source SHA mismatch');
}

/**
 * Build, scan, SBOM and publish both images under `${registry}/${name}:${sha}`.
 * @param {{ sha: string, registry: string, output: string, scanJson?: boolean }} options
 * @returns manifest image entries (same schema as release-manifest.json).
 */
export function buildScanPublish({ sha, registry, output, scanJson = false }) {
  const entries = [];
  for (const [name, file] of IMAGE_SOURCES) {
    const image = `${registry}/${name}`;
    const tag = `${image}:${sha}`;
    run('docker', ['build', '--build-arg', `SOURCE_SHA=${sha}`, '-f', file, '-t', tag, '.']);
    const labels = JSON.parse(run('docker', ['image', 'inspect', tag], true))[0].Config.Labels || {};
    if (labels['org.opencontainers.image.revision'] !== sha) throw Error('Source/image revision mismatch');
    const archive = resolve(output, `${name}.tar`);
    run('docker', ['save', tag, '-o', archive]);
    // No Docker socket mount: Trivy reads a single image archive, plus its dedicated cache.
    const scan = ['run', '--rm', '-v', `${archive}:/input.tar:ro`, '-v', 'crane-trivy-cache:/root/.cache/', '-v', `${output}:/output`, TRIVY, 'image', '--input', '/input.tar'];
    if (scanJson) run('docker', [...scan, '--scanners', 'vuln', '--format', 'json', '--output', `/output/${name}.trivy.json`]);
    run('docker', [...scan, '--scanners', 'vuln', '--severity', 'HIGH,CRITICAL', '--exit-code', '1']);
    const sbom = `${name}.cdx.json`;
    run('docker', [...scan, '--format', 'cyclonedx', '--output', '/output/' + sbom]);
    const sbomHash = createHash('sha256').update(readFileSync(resolve(output, sbom))).digest('hex');
    run('docker', ['push', tag]);
    const digests = JSON.parse(run('docker', ['image', 'inspect', tag], true))[0].RepoDigests || [];
    const reference = digests.find(value => value.startsWith(image + '@sha256:'));
    if (!reference || !/@sha256:[a-f0-9]{64}$/.test(reference)) throw Error('Registry did not return the exact published digest');
    entries.push({ source_sha: sha, image_name: image, image_digest: reference.split('@')[1], sbom_sha256: sbomHash, sbom_file: sbom, build_time: new Date().toISOString(), schema_version: 1 });
  }
  return entries;
}
