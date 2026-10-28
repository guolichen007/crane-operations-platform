import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, realpathSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { assertExactCheckout } from '../../deploy/scripts/source-check.mjs';
import { verifyRelease } from '../../deploy/scripts/release-check.mjs';
import { checkProductionDisks, checkDisk } from '../../deploy/scripts/disk-check.mjs';
import { upgrade } from '../../deploy/scripts/upgrade.mjs';
import { productionProject, validateProduction } from '../../tools/production-contract.mjs';

function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), 'crane-r1-'));
  t.after(() => {
    assert.equal(realpathSync(dirname(directory)), realpathSync(tmpdir()));
    assert.ok(basename(directory).startsWith('crane-r1-'));
    rmSync(directory, { recursive: true, force: true });
  });
  const root = join(directory, 'checkout'); mkdirSync(root);
  function git(...args) {
    const r = spawnSync('git', ['-C', root, '-c', 'user.name=Anonymous', '-c', 'user.email=ci@example.test', '-c', 'commit.gpgsign=false', ...args], { encoding: 'utf8' });
    assert.equal(r.status, 0, 'anonymous fixture Git operation failed');
    return r.stdout.trim();
  }
  git('init', '--quiet'); mkdirSync(join(root, 'deploy/compose'), { recursive: true });
  const deployment = join(root, 'deploy/compose/docker-compose.prod.yml'); writeFileSync(deployment, 'name: u-dao-crane-ops\n');
  git('add', '.'); git('commit', '--quiet', '-m', 'anonymous fixture');
  const sha = git('rev-parse', 'HEAD');
  const manifest = { schema_version: 1, source_sha: sha, images: ['cloud-api', 'web-console'].map(name => {
    const sbom_file = name + '.cdx.json', data = '{"bomFormat":"CycloneDX"}\n'; writeFileSync(join(directory, sbom_file), data);
    return { schema_version: 1, source_sha: sha, image_name: 'ghcr.io/guolichen007/crane-operations-platform/' + name, image_digest: 'sha256:' + 'a'.repeat(64), sbom_file, sbom_sha256: createHash('sha256').update(data).digest('hex') };
  }) };
  const manifestPath = join(directory, 'release-manifest.json');
  const save = () => writeFileSync(manifestPath, JSON.stringify(manifest)); save();
  const envFile = join(directory, 'production.env'); writeFileSync(envFile, `CRANE_BACKUP_ROOT=${directory.replaceAll('\\', '/')}\n`, { mode: 0o600 });
  return { root, directory, deployment, manifest, manifestPath, save, sha, envFile, git };
}
function config(manifest) {
  const services = {};
  for (const name of ['postgres', 'keycloak', 'mosquitto', 'cloud-api', 'web-console']) services[name] = { image: 'example.invalid/' + name + '@sha256:' + 'a'.repeat(64), logging: { driver: 'json-file', options: { 'max-size': '10m', 'max-file': '5' } } };
  for (const image of manifest.images) services[image.image_name.split('/').at(-1)].image = image.image_name + '@' + image.image_digest;
  services['cloud-api'].environment = { APP_ENV: 'production', OIDC_CLIENT_SECRET_FILE: '/run/secrets/oidc_client_secret' };
  services.keycloak.environment = { KC_HOSTNAME_STRICT: 'true', KC_DB: 'postgres' };
  return { name: productionProject, services, secrets: Object.fromEntries(Array.from({ length: 6 }, (_, index) => ['anonymous' + index, { file: '/anonymous/file' + index }])) };
}

test('manifest SHA different from actual checkout HEAD rejects before any Docker action', t => {
  const f = fixture(t); f.manifest.source_sha = 'b'.repeat(40); for (const image of f.manifest.images) image.source_sha = f.manifest.source_sha; f.save(); let calls = 0;
  assert.throws(() => upgrade('--apply', f.manifestPath, f.envFile, { root: f.root, execute: () => { calls++; }, diskGate: () => { calls++; } }), /differs from release source SHA/);
  assert.equal(calls, 0);
});
test('dirty tracked deployment configuration rejects production apply before Docker', t => {
  const f = fixture(t); writeFileSync(f.deployment, 'name: changed-project\n'); let calls = 0;
  assert.throws(() => upgrade('--apply', f.manifestPath, f.envFile, { root: f.root, execute: () => { calls++; }, diskGate: () => { calls++; } }), /clean Git worktree/);
  assert.equal(calls, 0);
});
test('exact clean SHA permits review and apply uses frozen project and anchored Compose path (fake Docker)', t => {
  const f = fixture(t), calls = [];
  assertExactCheckout(f.sha, f.root); assert.equal(verifyRelease(f.manifestPath, f.root).source_sha, f.sha);
  upgrade('--apply', f.manifestPath, f.envFile, { root: f.root, diskGate: options => { assert.equal(options.backupRoot, f.directory.replaceAll('\\', '/')); return {}; }, execute: (command, args, options) => {
    assert.equal(command, 'docker'); assert.equal(options.cwd, f.root); calls.push(args);
    return { status: 0, stdout: args.includes('config') ? JSON.stringify(config(f.manifest)) : '' };
  } });
  assert.equal(calls.length, 3);
  for (const args of calls) {
    assert.equal(args[args.indexOf('--project-name') + 1], 'u-dao-crane-ops');
    assert.equal(args[args.indexOf('-f') + 1], resolve(f.root, 'deploy/compose/docker-compose.prod.yml'));
  }
});
test('tracked deployment drift during pull blocks up (fake Docker)', t => {
  const f = fixture(t), calls = [];
  assert.throws(() => upgrade('--apply', f.manifestPath, f.envFile, { root: f.root, diskGate: () => ({}), execute: (_, args) => {
    calls.push(args); if (args.includes('pull')) writeFileSync(f.deployment, 'name: drift-during-pull\n');
    return { status: 0, stdout: args.includes('config') ? JSON.stringify(config(f.manifest)) : '' };
  } }), /clean Git worktree/);
  assert.equal(calls.some(args => args.includes('up')), false);
});

const actualDocker = resolve('/srv/u-dao/platform/docker'), backup = resolve('/srv/u-dao/backups/crane-ops');
const enough = { bsize: 1024n ** 3n, blocks: 100n, bavail: 20n };
function diskAdapters(seen = [], backupStats = enough) {
  return { canonical: resolve, directory: () => true, execute: (_, args) => {
    if (args[0] === 'context') { assert.deepEqual(args, ['context', 'inspect', '--format', '{{.Endpoints.docker.Host}}']); return { status: 0, stdout: 'unix:///var/run/docker.sock' }; }
    assert.deepEqual(args, ['info', '--format', '{{.DockerRootDir}}']); return { status: 0, stdout: actualDocker + '\n' };
  }, statfs: path => { seen.push(path); return path === backup ? backupStats : enough; } };
}
test('wrong path cannot impersonate daemon DockerRootDir even if it has enough free space', () => {
  const seen = [];
  assert.throws(() => checkProductionDisks({ backupRoot: backup, expectedDockerRoot: resolve('/var/lib/docker') }, diskAdapters(seen)), /differs from daemon/);
  assert.deepEqual(seen, []);
});
test('actual Docker root and backup filesystem are independently checked', () => {
  const seen = [], result = checkProductionDisks({ backupRoot: backup, expectedDockerRoot: actualDocker }, diskAdapters(seen));
  assert.equal(result.dockerRoot, actualDocker); assert.deepEqual(seen, [actualDocker, backup]);
  assert.throws(() => checkProductionDisks({ backupRoot: backup }, diskAdapters([], { ...enough, blocks: 50n, bavail: 9n })), /Insufficient free disk/);
  assert.throws(() => checkProductionDisks({ backupRoot: backup }, diskAdapters([], { ...enough, bavail: 14n })), /Insufficient free disk/);
});
test('unconfirmed Docker root, absent backup root and weakened thresholds fail closed', () => {
  for (const response of [{ status: 1 }, { status: 0, stdout: '' }, { status: 0, stdout: 'relative/path' }]) assert.throws(() => checkProductionDisks({ backupRoot: backup }, { execute: () => response }), /Cannot confirm actual DockerRootDir/);
  assert.throws(() => checkProductionDisks({}, diskAdapters()), /backup root required/);
  assert.throws(() => checkProductionDisks({ backupRoot: backup }, { ...diskAdapters(), directory: () => false }), /existing directories/);
  assert.throws(() => checkDisk(actualDocker, 9, 15, () => enough), /at least/);
  assert.throws(() => checkDisk(actualDocker, 10, 14, () => enough), /at least/);
});
test('remote Docker context cannot borrow a same-named local filesystem', () => {
  const seen = [], adapters = diskAdapters(seen), localExecute = adapters.execute;
  adapters.execute = (command, args) => args[0] === 'context' ? { status: 0, stdout: 'ssh://anonymous.example.test' } : localExecute(command, args);
  assert.throws(() => checkProductionDisks({ backupRoot: backup }, adapters), /confirmed local Docker daemon/);
  assert.deepEqual(seen, []);
});
test('old Compose project is rejected while the frozen production project passes', t => {
  const f = fixture(t), resolved = config(f.manifest); validateProduction(resolved);
  resolved.name = 'crane-ops'; assert.throws(() => validateProduction(resolved), /project is frozen/);
});
test('all tracked Dockerfile base images use explicit version and digest', () => {
  for (const name of readdirSync('deploy').filter(name => name.startsWith('Dockerfile'))) {
    const lines = readFileSync(join('deploy', name), 'utf8').split('\n').filter(line => /^FROM /i.test(line));
    assert.ok(lines.length);
    for (const line of lines) assert.match(line, /^FROM \S+:[^\s@]+@sha256:[a-f0-9]{64}(?: AS \S+)?$/i, name);
  }
});
