// H3-P G2: immutable, traceable Preview candidate images (never production images).
//
// Runs only in .github/workflows/preview-candidate.yml on a pushed tag `preview/h3-*`.
// It does NOT reuse tools/release.mjs and never writes the production image names
// (ghcr.io/<repo>/cloud-api, ghcr.io/<repo>/web-console): Preview images live under
// ghcr.io/<repo>/preview-cloud-api and ghcr.io/<repo>/preview-web-console, tagged with the full
// source SHA only (no `latest`, no moving tag). Acceptance always references image@sha256.
//
// Steps per image: build (SOURCE_SHA label) → verify revision label → archive → Trivy HIGH/CRITICAL
// (fails the run) → CycloneDX SBOM → push full-SHA tag → read the registry digest.
// Output: release/h3-preview/preview-manifest.json (+ SBOMs, Trivy JSON, uatdata binary hash).
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

export const PREVIEW_IMAGES = [
  ['preview-cloud-api', 'deploy/Dockerfile.backend'],
  ['preview-web-console', 'deploy/Dockerfile.web-production'],
];
export const TAG_PATTERN = /^preview\/h3-[a-z0-9][a-z0-9.-]{0,40}$/;

export function checkInputs(env) {
  const problems = [];
  if (!/^[a-f0-9]{40}$/.test(env.GITHUB_SHA || '')) problems.push('GITHUB_SHA must be a full 40-hex SHA');
  if ((env.GITHUB_REPOSITORY || '').toLowerCase() !== 'guolichen007/crane-operations-platform') problems.push('unexpected repository');
  if (env.GITHUB_REF_TYPE !== 'tag' || !TAG_PATTERN.test(env.GITHUB_REF_NAME || '')) problems.push('must run on a preview/h3-* tag');
  if (!/^[0-9]+$/.test(env.CI_RUN_ID || '')) problems.push('CI_RUN_ID (green Crane Platform CI run of the same SHA) required');
  return problems;
}

function versions() {
  const go = /^go (\S+)$/m.exec(readFileSync('go.mod', 'utf8'))?.[1];
  const backend = readFileSync('deploy/Dockerfile.backend', 'utf8');
  const web = readFileSync('deploy/Dockerfile.web-production', 'utf8');
  const upstream = JSON.parse(readFileSync('deploy/production/images.json', 'utf8'));
  return {
    go_module: go,
    go_build_image: /^FROM (golang:\S+)/m.exec(backend)?.[1],
    node_build_image: /^FROM (node:\S+)/m.exec(web)?.[1],
    runtime_base_image: /^FROM (alpine:\S+)/m.exec(backend)?.[1],
    postgres_image: upstream.postgres,
    keycloak_image: upstream.keycloak,
    mosquitto_image: upstream.mosquitto,
  };
}

function main() {
  const problems = checkInputs(process.env);
  if (problems.length) throw Error('Preview candidate refused: ' + problems.join('; '));
  const sha = process.env.GITHUB_SHA;
  const repository = process.env.GITHUB_REPOSITORY.toLowerCase();
  const output = resolve('release/h3-preview');
  mkdirSync(output, { recursive: true });
  const run = (command, args, capture = false) => {
    const result = spawnSync(command, args, { encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' });
    if (result.error || result.status) throw Error('Preview candidate command failed: ' + command + ' ' + args[0]);
    return result.stdout || '';
  };
  if (run('git', ['rev-parse', 'HEAD'], true).trim() !== sha) throw Error('Checkout/source SHA mismatch');
  const sha256 = (file) => createHash('sha256').update(readFileSync(file)).digest('hex');
  const images = [];
  for (const [name, file] of PREVIEW_IMAGES) {
    const image = `ghcr.io/${repository}/${name}`;
    const tag = `${image}:${sha}`;
    run('docker', ['build', '--pull=false', '--build-arg', `SOURCE_SHA=${sha}`, '-f', file, '-t', tag, '.']);
    const labels = JSON.parse(run('docker', ['image', 'inspect', tag], true))[0].Config.Labels || {};
    if (labels['org.opencontainers.image.revision'] !== sha) throw Error('Source/image revision mismatch: ' + name);
    const archive = resolve(output, `${name}.tar`);
    run('docker', ['save', tag, '-o', archive]);
    const scan = ['run', '--rm', '-v', `${archive}:/input.tar:ro`, '-v', 'crane-trivy-cache:/root/.cache/', '-v', `${output}:/output`, 'aquasec/trivy:0.74.0', 'image', '--input', '/input.tar'];
    run('docker', [...scan, '--scanners', 'vuln', '--format', 'json', '--output', `/output/${name}.trivy.json`]);
    run('docker', [...scan, '--scanners', 'vuln', '--severity', 'HIGH,CRITICAL', '--exit-code', '1']);
    run('docker', [...scan, '--format', 'cyclonedx', '--output', `/output/${name}.cdx.json`]);
    run('docker', ['push', tag]);
    const digests = JSON.parse(run('docker', ['image', 'inspect', tag], true))[0].RepoDigests || [];
    const reference = digests.find((value) => value.startsWith(image + '@sha256:'));
    if (!reference || !/@sha256:[a-f0-9]{64}$/.test(reference)) throw Error('Registry did not return the published digest: ' + name);
    images.push({
      name, image, reference, digest: reference.split('@')[1], source_sha: sha,
      sbom_file: `${name}.cdx.json`, sbom_sha256: sha256(resolve(output, `${name}.cdx.json`)),
      trivy_file: `${name}.trivy.json`, trivy_gate: 'HIGH,CRITICAL=0',
    });
  }
  const uatdata = resolve(output, 'uatdata-linux-amd64');
  const build = spawnSync('go', ['build', '-trimpath', '-ldflags=-s -w', '-o', uatdata, './tools/uatdata'], {
    stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0', GOOS: 'linux', GOARCH: 'amd64' },
  });
  if (build.status) throw Error('uatdata build failed');
  const manifest = {
    schema_version: 1,
    kind: 'h3-preview-candidate',
    source_sha: sha,
    tag: process.env.GITHUB_REF_NAME,
    ci_run_id: Number(process.env.CI_RUN_ID),
    candidate_run_id: Number(process.env.GITHUB_RUN_ID || 0),
    built_at: new Date().toISOString(),
    images,
    tools: [{ name: 'uatdata', file: 'uatdata-linux-amd64', sha256: sha256(uatdata) }],
    versions: versions(),
    not_production: 'Preview images only; the production publishing job and image names are untouched',
  };
  writeFileSync(resolve(output, 'preview-manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
  const env = images.map((i) => `${i.name === 'preview-cloud-api' ? 'CRANE_CLOUD_IMAGE' : 'CRANE_WEB_IMAGE'}=${i.reference}`).join('\n');
  writeFileSync(resolve(output, 'preview-images.env'), `# source ${sha}\n${env}\n`);
  console.log('PREVIEW_CANDIDATE=PASS SOURCE_SHA=' + sha);
}

if (import.meta.url === `file://${process.argv[1]}`) main();
