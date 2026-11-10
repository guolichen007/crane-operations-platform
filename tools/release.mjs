// CI-only production release (main push only): shared build chain in tools/release-lib.mjs.
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { assertSource, buildScanPublish } from './release-lib.mjs';

const sha = process.env.GITHUB_SHA;
const repository = (process.env.GITHUB_REPOSITORY || '').toLowerCase();
assertSource(sha, repository);
const output = resolve('release/phase1-a1'); mkdirSync(output, { recursive: true });
const entries = buildScanPublish({ sha, registry: `ghcr.io/${repository}`, output });
const manifest = { schema_version: 1, source_sha: sha, images: entries, upstream: JSON.parse(readFileSync('deploy/production/images.json', 'utf8')) };
writeFileSync(resolve(output, 'release-manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
console.log('RELEASE=PASS SOURCE_SHA=' + sha);
