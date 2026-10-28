// --apply is only for a separately authorized server change window after HTTPS bootstrap.
import { spawnSync } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';
import { resolve } from 'node:path';
import { parseEnv } from 'node:util';
import { productionProject, validateProduction } from '../../tools/production-contract.mjs';
import { verifyRelease } from './release-check.mjs';
import { assertExactCheckout, repositoryRoot } from './source-check.mjs';
import { checkProductionDisks } from './disk-check.mjs';

export function upgrade(mode, manifestArgument, environmentArgument, { root = repositoryRoot, execute = spawnSync, diskGate = checkProductionDisks } = {}) {
  if (!['--check', '--apply'].includes(mode) || !manifestArgument || !environmentArgument) throw Error('Usage: upgrade.mjs --check|--apply manifest.json private-production.env');
  const manifest = verifyRelease(resolve(manifestArgument), root);
  const envFile = resolve(environmentArgument);
  if (process.platform !== 'win32' && statSync(envFile).mode & 0o077) throw Error('Private production env requires mode 0600');
  const environment = parseEnv(readFileSync(envFile, 'utf8'));
  if (environment.CRANE_DISK_PATH || process.env.CRANE_DISK_PATH) throw Error('CRANE_DISK_PATH is retired; DockerRootDir must be discovered');
  const disks = diskGate({ backupRoot: environment.CRANE_BACKUP_ROOT, expectedDockerRoot: environment.CRANE_DOCKER_ROOT_DIR });
  function run(args, capture = false) {
    const result = execute('docker', args, { cwd: root, encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' });
    if (result.error || result.status !== 0) throw Error('Upgrade preflight/action failed');
    return result.stdout || '';
  }
  const compose = ['compose', '--project-name', productionProject, '--project-directory', resolve(root, 'deploy/compose'), '--env-file', envFile, '-f', resolve(root, 'deploy/compose/docker-compose.prod.yml')];
  const config = JSON.parse(run([...compose, 'config', '--format', 'json'], true));
  validateProduction(config);
  for (const item of manifest.images) {
    const service = item.image_name.endsWith('/cloud-api') ? 'cloud-api' : 'web-console';
    if (config.services[service].image !== item.image_name + '@' + item.image_digest) throw Error('Production env does not match reviewed release digest');
  }
  if (mode === '--apply') {
    assertExactCheckout(manifest.source_sha, root);
    run([...compose, 'pull']);
    // Pull can take minutes: recheck tools/Compose/migrations immediately before up.
    assertExactCheckout(manifest.source_sha, root);
    run([...compose, 'up', '-d', '--no-build', '--wait', 'postgres', 'keycloak', 'mosquitto', 'cloud-api', 'web-console']);
  }
  console.log('UPGRADE_PREFLIGHT=PASS mode=' + mode + ' project=' + productionProject);
  return { manifest, disks };
}
if (import.meta.main) {
  try { upgrade(...process.argv.slice(2)); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
