import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve, dirname, join } from 'node:path';
import { createHash, randomUUID } from 'node:crypto';
function run(cmd, args, cwd = process.cwd()) {
  const r = spawnSync(cmd, args, { cwd, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 });
  if (r.status) throw Error('Release tool failed: ' + cmd);
  return r.stdout;
}
const sha = run('git', ['rev-parse', 'HEAD']).trim(),
  branch = run('git', ['branch', '--show-current']).trim();
if (branch !== 'product/baobei-v1' || run('git', ['status', '--porcelain']).trim())
  throw Error('Release evidence requires clean product branch');
const out = resolve('local-private/baobei/release', sha);
mkdirSync(out, { recursive: true });
const docker = process.env.BAOBEI_DOCKER_CONTEXT
  ? ['--context', process.env.BAOBEI_DOCKER_CONTEXT]
  : [];
const image = (name) =>
  JSON.parse(run('docker', [...docker, 'image', 'inspect', 'u-dao-baobei-' + name]))[0];
const web = image('baobei-web'),
  api = image('baobei-api');
for (const i of [web, api])
  if (i.Config.Labels?.['org.opencontainers.image.revision'] !== sha)
    throw Error('Image source SHA does not match HEAD; rebuild with BAOBEI_SOURCE_SHA');
const npmCli = join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js');
const sbom =
  process.platform === 'win32'
    ? run(
        process.execPath,
        [npmCli, 'sbom', '--sbom-format', 'cyclonedx', '--omit=dev'],
        resolve('apps/project-intake-web')
      )
    : run(
        'npm',
        ['sbom', '--sbom-format', 'cyclonedx', '--omit=dev'],
        resolve('apps/project-intake-web')
      );
JSON.parse(sbom);
writeFileSync(join(out, 'web.cdx.json'), sbom);
const binary = resolve('local-private/baobei-api' + (process.platform === 'win32' ? '.exe' : ''));
const deps = run(process.env.BAOBEI_GO || 'go', ['version', '-m', binary])
  .split('\n')
  .map((s) => s.trim().split(/\s+/))
  .filter((xs) => xs[0] === 'dep');
const goSBOM = {
  bomFormat: 'CycloneDX',
  specVersion: '1.6',
  serialNumber: 'urn:uuid:' + randomUUID(),
  version: 1,
  metadata: {
    timestamp: new Date().toISOString(),
    component: { type: 'application', name: 'project-intake-api', version: sha },
  },
  components: deps.map(([, name, version]) => ({
    type: 'library',
    name,
    version,
    purl: 'pkg:golang/' + name + '@' + version,
  })),
};
if (!deps.length) throw Error('Go module inventory empty');
writeFileSync(join(out, 'api.cdx.json'), JSON.stringify(goSBOM, null, 2));
const hash = (file) => createHash('sha256').update(readFileSync(file)).digest('hex');
const manifest = {
  source_sha: sha,
  branch,
  build_time: new Date().toISOString(),
  web_image_digest: web.Id,
  api_image_digest: api.Id,
  digest_kind: 'local_image_id',
  registry_published: false,
  schema_version: '00002',
  openapi_hash: hash('contracts/project-intake/openapi.yaml'),
  transfer_contract_hash: hash('contracts/project-intake/ProjectIntakeTransferV1.schema.json'),
  web_sbom_hash: hash(join(out, 'web.cdx.json')),
  api_sbom_hash: hash(join(out, 'api.cdx.json')),
};
writeFileSync(join(out, 'manifest.json'), JSON.stringify(manifest, null, 2));
console.log('Release manifest and two CycloneDX SBOMs generated for ' + sha);
