// CI-only release: build -> tar scan -> SBOM -> publish full-SHA tag -> immutable registry digest.
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const sha = process.env.GITHUB_SHA;
if (!/^[a-f0-9]{40}$/.test(sha || '')) throw Error('Release requires exact GITHUB_SHA');
const repository = (process.env.GITHUB_REPOSITORY || '').toLowerCase();
if (repository !== 'guolichen007/crane-operations-platform') throw Error('Unexpected release repository');
const output = resolve('release/phase1-a1'); mkdirSync(output, { recursive: true });
function run(command, args, capture = false) {
  const result = spawnSync(command, args, { encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' });
  if (result.error || result.status) throw Error('Release command failed: ' + command);
  return result.stdout || '';
}
const entries = [];
if (run('git', ['rev-parse', 'HEAD'], true).trim() !== sha) throw Error('Checkout/source SHA mismatch');
for (const [name, file] of [['cloud-api', 'deploy/Dockerfile.backend'], ['web-console', 'deploy/Dockerfile.web-production']]) {
  const image = `ghcr.io/${repository}/${name}`;
  const tag = `${image}:${sha}`;
  run('docker', ['build', '--build-arg', `SOURCE_SHA=${sha}`, '-f', file, '-t', tag, '.']);
  const labels = JSON.parse(run('docker', ['image', 'inspect', tag], true))[0].Config.Labels;
  if (labels['org.opencontainers.image.revision'] !== sha) throw Error('Source/image revision mismatch');
  const archive = resolve(output, `${name}.tar`);
  run('docker', ['save', tag, '-o', archive]);
  // No Docker socket mount: Trivy reads a single image archive, plus its dedicated cache.
  const scan = ['run', '--rm', '-v', `${archive}:/input.tar:ro`, '-v', 'crane-trivy-cache:/root/.cache/', '-v', `${output}:/output`, 'aquasec/trivy:0.74.0', 'image', '--input', '/input.tar'];
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
const manifest = { schema_version: 1, source_sha: sha, images: entries, upstream: JSON.parse(readFileSync('deploy/production/images.json', 'utf8')) };
writeFileSync(resolve(output, 'release-manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
console.log('RELEASE=PASS SOURCE_SHA=' + sha);
