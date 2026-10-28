// Real Keycloak + production BFF/Web + mTLS MQTT, all on an isolated anonymous Compose project.
import { spawnSync } from 'node:child_process';
import { createServer } from 'node:net';
import { mkdirSync, writeFileSync, existsSync, readFileSync, readdirSync, chmodSync } from 'node:fs';
import { resolve, dirname, join } from 'node:path';
const docker = process.platform === 'win32' ? 'C:/Program Files/Docker/Docker/resources/bin/docker.exe' : 'docker';
const go = existsSync('tools/.cache/go/bin/go.exe') ? resolve('tools/.cache/go/bin/go.exe') : 'go';
const directory = resolve(`deploy/compose/runtime/a1-production-${process.pid}`); mkdirSync(directory, { recursive: true, mode: 0o700 });
const env = { ...process.env, GOCACHE: resolve('tools/.cache/gobuild'), GOMODCACHE: resolve('tools/.cache/gomod'), GOPATH: resolve('tools/.cache/gopath') };
function run(command, args, extra = {}, capture = false) { const result = spawnSync(command, args, { env: { ...env, ...extra }, encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' }); if (result.error || result.status) throw Error('A1 production fixture command failed: ' + command); return result.stdout || ''; }
async function port() { const server = createServer(); await new Promise(resolve => server.listen(0, '127.0.0.1', resolve)); const selected = server.address().port; await new Promise(resolve => server.close(resolve)); return selected; }
const [webPort, authPort, directWebPort, directAuthPort] = await Promise.all([port(), port(), port(), port()]);
run(go, ['run', './tests/integration/a1fixture', '--out', directory]);
run(go, ['build', '-trimpath', '-o', join(directory, 'a1-fixture'), './tests/integration/a1fixture'], { GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' });
const webOrigin = `https://crane.test:${webPort}`, authOrigin = `https://auth.crane.test:${authPort}`;
const configuration = {
  CRANE_CLOUD_IMAGE: process.env.A1_CLOUD_IMAGE || 'crane-a1-cloud:local', CRANE_WEB_IMAGE: process.env.A1_WEB_IMAGE || 'crane-a1-web:local',
  CRANE_PUBLIC_URL: webOrigin, CRANE_AUTH_URL: authOrigin, CRANE_WEB_PORT: directWebPort, CRANE_AUTH_PORT: directAuthPort,
  CRANE_PROXY_CIDRS: '172.16.0.0/12,192.168.0.0/16', CRANE_SECRET_DIR: join(directory, 'secrets').replaceAll('\\', '/'),
  CRANE_MQTT_DIR: directory.replaceAll('\\', '/'), CRANE_MQTT_ACL: join(directory, 'acl').replaceAll('\\', '/'),
  CRANE_INVENTORY_FILE: join(directory, 'inventory.json').replaceAll('\\', '/'), CRANE_FLEET_FILE: join(directory, 'fleet.json').replaceAll('\\', '/'),
  A1_PROD_FIXTURE_DIR: directory.replaceAll('\\', '/'), A1_WEB_PORT: webPort, A1_AUTH_PORT: authPort,
};
const envFile = join(directory, '.env'); writeFileSync(envFile, Object.entries(configuration).map(([key, value]) => `${key}=${value}`).join('\n') + '\n', { mode: 0o600 }); chmodSync(envFile, 0o600);
writeFileSync(join(directory, 'mosquitto.conf'), readFileSync('deploy/mosquitto/mosquitto.prod.conf', 'utf8').replaceAll('/mosquitto/certs', '/fixture/broker').replace('/mosquitto/acl', '/fixture/acl'));
writeFileSync(join(directory, 'ingress.conf'), `events {}\nhttp { resolver 127.0.0.11 valid=5s; access_log off; error_log /dev/stderr warn;
map $http_upgrade $upgrade { default upgrade; '' close; }
server { listen ${webPort} ssl; server_name crane.test; ssl_certificate /fixture/ingress/ingress.crt; ssl_certificate_key /fixture/ingress/ingress.key;
location / { set $web web-console:8080; proxy_pass http://$web; proxy_http_version 1.1; proxy_set_header Host $http_host; proxy_set_header X-Forwarded-Host $http_host; proxy_set_header X-Forwarded-Proto https; proxy_set_header X-Forwarded-For $remote_addr; proxy_set_header Forwarded ''; proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection $upgrade; }}
server { listen ${authPort} ssl; server_name auth.crane.test; ssl_certificate /fixture/ingress/ingress.crt; ssl_certificate_key /fixture/ingress/ingress.key;
location / { proxy_pass http://keycloak:8080; proxy_set_header Host $http_host; proxy_set_header X-Forwarded-Host $http_host; proxy_set_header X-Forwarded-Proto https; proxy_set_header X-Forwarded-For $remote_addr; proxy_set_header Forwarded ''; }} }\n`);
const compose = ['compose', '--project-name', `crane-a1-prod-${process.pid}`, '--env-file', envFile, '-f', 'deploy/compose/docker-compose.prod.yml', '-f', 'tests/integration/a1-production/compose.yml'];
try {
  run(docker, [...compose, 'config', '--quiet']);
  run(docker, [...compose, 'run', '--rm', '--no-deps', 'fixture-init']);
  run(docker, [...compose, 'up', '-d', '--wait', '--wait-timeout', '300', 'postgres', 'keycloak', 'mosquitto', 'ingress']);
  run(docker, [...compose, 'run', '--rm', '--no-deps', 'provisioner']);
  run(docker, [...compose, 'run', '--rm', '--no-deps', 'provisioner']); // Repeat must be safe and retain exact client secret.
  run(docker, [...compose, 'up', '-d', '--wait', '--wait-timeout', '180', 'cloud-api', 'web-console']);
  run(docker, [...compose, 'run', '--rm', '--no-deps', 'fixture-tool', '--operation', 'seed']);
  const state = run(docker, [...compose, 'ps', '--services'], {}, true).trim().split('\n');
  if (state.some(name => /simulator|remote|pointcloud/.test(name))) throw Error('Unexpected production fixture service');
  const npm = process.platform === 'win32' ? [process.execPath, [join(dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'), 'exec', '--', 'playwright', 'test', '--config', 'playwright.auth.config.ts']] : ['npx', ['playwright', 'test', '--config', 'playwright.auth.config.ts']];
  run(npm[0], npm[1], { A1_WEB_ORIGIN: webOrigin, A1_AUTH_ORIGIN: authOrigin, A1_BROWSER_PASSWORD_FILE: join(directory, 'secrets/browser_test_password') });
  // Backup CLI uses the same isolated project through a generated resolved Compose file; no secrets are emitted.
  const resolvedFile = join(directory, 'resolved.yml'); writeFileSync(resolvedFile, run(docker, [...compose, 'config'], {}, true), { mode: 0o600 });
  const python = process.platform === 'win32' ? 'python' : 'python3';
  const backupRoot = join(directory, 'backups'), sourceSHA = run('git', ['rev-parse', 'HEAD'], {}, true).trim();
  const backupArgs = ['deploy/scripts/backup.py', '--compose', resolvedFile, '--env-file', envFile, '--docker', docker];
  run(python, [...backupArgs, 'backup', '--root', backupRoot, '--source-sha', sourceSHA]);
  const completed = readdirSync(backupRoot).find(name => name.startsWith('crane-backup-')); if (!completed) throw Error('Missing complete backup set');
  run(python, [...backupArgs, 'drill', '--backup', join(backupRoot, completed)]);
  const evidenceDirectory = resolve('local-private/validation/phase1-a1'); mkdirSync(evidenceDirectory, { recursive: true, mode: 0o700 });
  writeFileSync(join(evidenceDirectory, 'production-integration.json'), JSON.stringify({ status: 'PASS', source_sha: sourceSHA, keycloak: '26.8.1', browser_tests: 3, backup: 'PASS', restore: 'PASS', production_deployed: false }, null, 2), { mode: 0o600 });
  console.log('A1_PRODUCTION_INTEGRATION=PASS KEYCLOAK=REAL E2E_AUTH=PASS BACKUP=PASS RESTORE=PASS');
} finally {
  run(docker, [...compose, 'down', '--volumes', '--remove-orphans']);
}
