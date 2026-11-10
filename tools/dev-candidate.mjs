// D1 development candidate for https://crane.youdaozd.com (development stage, no real users,
// no real devices). Runs only from .github/workflows/dev-candidate.yml on a `dev/crane-*` tag
// whose exact SHA already has a green nine-job "Crane Platform CI" run.
//
// It reuses the production build chain (tools/release-lib.mjs) unchanged, but publishes under a
// separate namespace: ghcr.io/<repo>/dev/cloud-api|web-console:<full sha>. The production names
// ghcr.io/<repo>/cloud-api|web-console are written only by production-release on main.
// Output release/dev-candidate/dev-manifest.json is accepted by deploy/scripts/dev-upgrade.mjs.
// BUILD SUCCESS ≠ DEPLOYMENT APPROVAL: nothing here touches a server.
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { assertSource, buildScanPublish, REPOSITORY } from './release-lib.mjs';

export const DEV_TAG = /^dev\/crane-[a-z0-9][a-z0-9.-]{0,40}$/;
export const DEV_REGISTRY = `ghcr.io/${REPOSITORY}/dev`;
export const DEV_MANIFEST_KIND = 'crane-dev-candidate';

export function checkInputs(env) {
  const problems = [];
  if (!/^[a-f0-9]{40}$/.test(env.GITHUB_SHA || '')) problems.push('GITHUB_SHA must be a full 40-hex SHA');
  if ((env.GITHUB_REPOSITORY || '').toLowerCase() !== REPOSITORY) problems.push('unexpected repository');
  if (env.GITHUB_REF_TYPE !== 'tag' || !DEV_TAG.test(env.GITHUB_REF_NAME || '') || env.GITHUB_REF_NAME.includes('..')) problems.push('must run on a dev/crane-* tag');
  if (!/^[0-9]+$/.test(env.CI_RUN_ID || '')) problems.push('CI_RUN_ID (green Crane Platform CI run of the same SHA) required');
  return problems;
}

export function versions() {
  const backend = readFileSync('deploy/Dockerfile.backend', 'utf8');
  const web = readFileSync('deploy/Dockerfile.web-production', 'utf8');
  const upstream = JSON.parse(readFileSync('deploy/production/images.json', 'utf8'));
  return {
    go_module: /^go (\S+)$/m.exec(readFileSync('go.mod', 'utf8'))?.[1],
    go_build_image: /^FROM (golang:\S+)/m.exec(backend)?.[1],
    node_build_image: /^FROM (node:\S+)/m.exec(web)?.[1],
    runtime_base_image: /^FROM (alpine:\S+)/m.exec(backend)?.[1],
    postgres_image: upstream.postgres, keycloak_image: upstream.keycloak, mosquitto_image: upstream.mosquitto,
  };
}

/** Highest goose migration version embedded in this source tree (deploy/postgres/migrations). */
export function schemaVersion(files) {
  const versions = files.map(f => /^(\d{5})_.+\.sql$/.exec(f)?.[1]).filter(Boolean).map(Number);
  if (!versions.length) throw Error('No migrations found');
  return Math.max(...versions);
}

async function main() {
  const problems = checkInputs(process.env);
  if (problems.length) throw Error('Dev candidate refused: ' + problems.join('; '));
  const sha = process.env.GITHUB_SHA;
  assertSource(sha, process.env.GITHUB_REPOSITORY);
  const output = resolve('release/dev-candidate'); mkdirSync(output, { recursive: true });
  const images = buildScanPublish({ sha, registry: DEV_REGISTRY, output, scanJson: true });
  const { readdirSync } = await import('node:fs');
  const manifest = {
    schema_version: 1, kind: DEV_MANIFEST_KIND, source_sha: sha, tag: process.env.GITHUB_REF_NAME,
    ci_run_id: Number(process.env.CI_RUN_ID), candidate_run_id: Number(process.env.GITHUB_RUN_ID || 0),
    schema_version_max: schemaVersion(readdirSync('deploy/postgres/migrations')),
    images, versions: versions(), upstream: JSON.parse(readFileSync('deploy/production/images.json', 'utf8')),
    deployment: 'NOT_APPROVED — building a candidate never approves deployment',
  };
  writeFileSync(resolve(output, 'dev-manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
  console.log('DEV_CANDIDATE_BUILD=PASS SOURCE_SHA=' + sha + ' DEPLOYMENT=NOT_APPROVED');
}

if (import.meta.main) {
  try { await main(); } catch (error) { console.error(error.message); process.exitCode = 1; }
}
