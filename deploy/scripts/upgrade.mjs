// Run --check for review. --apply is only for a separately authorized server change window.
import { spawnSync } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';
import { resolve } from 'node:path';
import { validateProduction } from '../../tools/production-contract.mjs';
const [mode, manifestArgument, environmentArgument] = process.argv.slice(2);
if (!['--check', '--apply'].includes(mode) || !manifestArgument || !environmentArgument) throw Error('Usage: upgrade.mjs --check|--apply manifest.json private-production.env');
const manifestPath = resolve(manifestArgument), envFile = resolve(environmentArgument);
if (process.platform !== 'win32' && statSync(envFile).mode & 0o077) throw Error('Private production env requires mode 0600');
function run(command, args, capture = false) {
  const result = spawnSync(command, args, { encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' });
  if (result.error || result.status) throw Error('Upgrade preflight/action failed');
  return result.stdout || '';
}
// Disk and SBOM/source checks must succeed before any pull or up mutation.
run(process.execPath, ['deploy/scripts/release-check.mjs', manifestPath]);
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
const compose = ['compose', '--env-file', envFile, '-f', 'deploy/compose/docker-compose.prod.yml'];
const config = JSON.parse(run('docker', [...compose, 'config', '--format', 'json'], true));
validateProduction(config);
for (const item of manifest.images) {
  const service = item.image_name.endsWith('/cloud-api') ? 'cloud-api' : 'web-console';
  if (config.services[service].image !== item.image_name + '@' + item.image_digest) throw Error('Production env does not match reviewed release digest');
}
if (mode === '--apply') {
  run('docker', [...compose, 'pull']);
  run('docker', [...compose, 'up', '-d', '--no-build', '--wait', 'postgres', 'keycloak', 'mosquitto', 'cloud-api', 'web-console']);
}
console.log('UPGRADE_PREFLIGHT=PASS mode=' + mode);
