import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { setTimeout } from 'node:timers/promises';

const context = process.env.BAOBEI_DOCKER_CONTEXT;
const nginx = 'nginx:1.30.4-alpine';
const tools = 'baobei-cert-test:local';
const domain = 'baobei.youdaozd.com';
const prefix = '/.well-known/acme-challenge/';
const suffix = randomUUID();
const frontend = 'baobei-http01-front-' + suffix;
const internal = 'baobei-http01-back-' + suffix;
const containers = [];
const networks = [];
const scratchRoot = resolve('local-private/baobei');
mkdirSync(scratchRoot, { recursive: true });
const scratch = mkdtempSync(join(scratchRoot, 'http01-'));
const tls = join(scratch, 'tls');
const cert = join(tls, 'baobei', 'fullchain.pem');
const webroot = join(scratch, 'webroot');
const challenge = join(webroot, '.well-known', 'acme-challenge');
const vhost = resolve('deploy/baobei/ingress/baobei.youdaozd.com.conf.example');
const common = resolve('deploy/baobei/ingress/baobei-http-common.conf.example');
const ingressConfig = join(scratch, 'ingress-conf');
const firebot = join(ingressConfig, 'firebot.conf');
const duplicate = join(scratch, 'duplicate.conf');

function docker(args) {
  return spawnSync('docker', [...(context ? ['--context', context] : []), ...args], {
    encoding: 'utf8',
    timeout: 120_000,
  });
}

function checked(args) {
  const result = docker(args);
  if (result.error) throw result.error;
  assert.equal(result.status, 0, result.stderr || 'Docker fixture command failed');
  return result.stdout;
}

function bind(source, target, readOnly = true) {
  return ['--mount', 'type=bind,src=' + source + ',dst=' + target + (readOnly ? ',readonly' : '')];
}

function start(role, args) {
  const name = 'baobei-http01-' + role + '-' + suffix;
  containers.push(name);
  checked(['run', '--pull', 'never', '-d', '--name', name, ...args]);
  return name;
}

function inspect(name) {
  return JSON.parse(checked(['inspect', name]))[0];
}

async function ready(args, description) {
  for (let i = 0; i < 45; i++) {
    if (docker(args).status === 0) return;
    await setTimeout(1000);
  }
  assert.fail(description + ' did not become ready');
}

function ingressMounts(commonFile, includeFirebot) {
  return [
    ...bind(tls, '/etc/nginx/tls'),
    ...bind(commonFile, '/etc/nginx/conf.d/00-baobei-common.conf'),
    ...bind(vhost, '/etc/nginx/conf.d/baobei.conf'),
    ...(includeFirebot ? bind(firebot, '/etc/nginx/conf.d/firebot.conf') : []),
  ];
}

function syntax(commonFile, includeFirebot) {
  return ['run', '--pull', 'never', '--rm', '--network', 'none',
    ...ingressMounts(commonFile, includeFirebot), '--entrypoint', 'nginx', nginx, '-t'];
}

function request(probe, scheme, path, method = 'GET') {
  const port = scheme === 'http' ? 80 : 443;
  const raw = checked([
    'exec', probe, 'curl', '--silent', '--show-error', '--path-as-is',
    '--noproxy', '*', '--max-time', '10', '--connect-timeout', '3',
    '--cacert', '/fixture-ca.pem',
    '--connect-to', domain + ':' + port + ':shared-ingress:' + port,
    '--dump-header', '-', '--output', '-',
    ...(method === 'HEAD' ? ['--head', '--output', '/dev/null'] : ['--request', method]),
    scheme + '://' + domain + path,
  ]);
  const boundary = raw.indexOf('\r\n\r\n');
  assert(boundary >= 0, 'HTTP response lacks header boundary');
  const [status, ...lines] = raw.slice(0, boundary).split('\r\n');
  const headers = Object.fromEntries(lines.map(line => {
    const colon = line.indexOf(':');
    return [line.slice(0, colon).toLowerCase(), line.slice(colon + 1).trim()];
  }));
  return { status: Number(status.split(' ')[1]), headers, body: raw.slice(boundary + 4) };
}

try {
  mkdirSync(join(tls, 'baobei'), { recursive: true });
  mkdirSync(challenge, { recursive: true });
  mkdirSync(join(challenge, 'directory'));
  mkdirSync(ingressConfig);
  writeFileSync(join(webroot, '.well-known', 'outside'), 'synthetic-outside-challenge');
  // A representative HTTP-level layout from firebot.conf:7, isolated from the real configuration.
  writeFileSync(firebot, '# Synthetic Firebot HTTP include\n'.repeat(6) +
    'resolver 127.0.0.11 valid=10s ipv6=off;\nresolver_timeout 5s;\n' +
    'server { listen 80; server_name firebot.synthetic.test; return 204; }\n');
  writeFileSync(duplicate, 'resolver 127.0.0.11;\nresolver_timeout 5s;\n');
  checked([
    'run', '--pull', 'never', '--rm', '--network', 'none', ...bind(tls, '/fixture-tls', false),
    '--entrypoint', 'openssl', tools, 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
    '-days', '1', '-subj', '/CN=' + domain, '-addext', 'subjectAltName=DNS:' + domain,
    '-keyout', '/fixture-tls/baobei/privkey.pem', '-out', '/fixture-tls/baobei/fullchain.pem',
  ]);
  for (const includeFirebot of [false, true]) {
    checked(syntax(common, includeFirebot));
  }
  const conflict = docker(syntax(duplicate, true));
  assert.notEqual(conflict.status, 0);
  assert.match(conflict.stderr, /"resolver" directive is duplicate/);
  console.log('SHARED_NGINX_CONFLICT_TEST=PASS (1.30.4; standalone, combined, duplicate negative)');

  for (const name of [frontend, internal]) {
    checked(['network', 'create', '--internal', '--ipv6=false', name]);
    networks.push(name);
    const network = inspect(name);
    assert.equal(network.Internal, true);
    assert.equal(network.EnableIPv6, false);
  }
  const pg = start('pg', [
    '--network', internal, '--network-alias', 'baobei-postgres',
    '--tmpfs', '/var/lib/postgresql', '-e', 'POSTGRES_USER=baobei', '-e', 'POSTGRES_DB=baobei',
    '-e', 'POSTGRES_HOST_AUTH_METHOD=trust', 'postgres:18.4-alpine',
  ]);
  await ready(['exec', pg, 'pg_isready', '-h', '127.0.0.1', '-U', 'baobei', '-d', 'baobei'], 'PostgreSQL');
  const api = start('api', [
    '--network', internal, '--network-alias', 'baobei-api',
    '-e', 'APP_ENV=development', '-e', 'DEV_AUTH=0', '-e', 'LISTEN_ADDR=0.0.0.0:8088',
    '-e', 'DATABASE_URL=postgres://baobei@baobei-postgres/baobei?sslmode=disable',
    'u-dao-baobei-baobei-api',
  ]);
  await ready(['exec', api, 'wget', '-qO-', 'http://127.0.0.1:8088/health/ready'], 'Baobei API');
  const web = start('web', [
    '--network', frontend, '--network-alias', 'baobei-web', '--read-only',
    '--tmpfs', '/var/cache/nginx', '--tmpfs', '/var/run', '--tmpfs', '/tmp',
    ...bind(webroot, '/var/www/acme'),
    ...bind(resolve('deploy/baobei/nginx.production.conf'), '/etc/nginx/conf.d/default.conf'),
    ...bind(resolve('deploy/baobei/nginx.proxy.production.conf'), '/etc/nginx/baobei-proxy.conf'),
    'u-dao-baobei-baobei-web',
  ]);
  checked(['network', 'connect', internal, web]);
  const ingress = start('ingress', [
    '--network', frontend, '--network-alias', 'shared-ingress',
    ...bind(tls, '/etc/nginx/tls'), ...bind(ingressConfig, '/etc/nginx/conf.d'), nginx,
  ]);
  const probe = start('probe', [
    '--network', frontend, ...bind(cert, '/fixture-ca.pem'), '--entrypoint', 'sleep', tools, '3600',
  ]);
  const firebotProbe = ['exec', probe, 'curl', '--fail', '--silent', '--noproxy', '*', '--max-time', '3',
    '--header', 'Host: firebot.synthetic.test', '--write-out', '%{http_code}', 'http://shared-ingress/'];
  await ready(firebotProbe, 'Existing shared ingress');
  assert.equal(checked(firebotProbe), '204');
  const existingIngress = inspect(ingress);
  writeFileSync(join(ingressConfig, '00-baobei-common.conf'), readFileSync(common));
  writeFileSync(join(ingressConfig, 'baobei.conf'), readFileSync(vhost));
  checked(['exec', ingress, 'nginx', '-t']);
  checked(['exec', ingress, 'nginx', '-s', 'reload']);
  for (const name of [web, ingress]) {
    assert.match(docker(['exec', name, 'nginx', '-v']).stderr, /nginx\/1\.30\.4\b/);
    checked(['exec', name, 'nginx', '-t']);
  }
  await ready(['exec', probe, 'curl', '--fail', '--silent', '--noproxy', '*', '--max-time', '3',
    '--cacert', '/fixture-ca.pem', '--connect-to', domain + ':443:shared-ingress:443',
    'https://' + domain + '/health/ready'], 'Shared HTTPS');

  for (const name of [ingress, web, api, pg, probe]) {
    assert.equal(Object.keys(inspect(name).HostConfig.PortBindings ?? {}).length, 0);
  }
  const attachments = (name) => Object.keys(inspect(name).NetworkSettings.Networks).sort();
  assert.deepEqual(attachments(ingress), [frontend]);
  assert.deepEqual(attachments(web), [frontend, internal].sort());
  assert.deepEqual(attachments(api), [internal]);
  assert.deepEqual(attachments(pg), [internal]);
  const reloaded = inspect(ingress);
  assert.equal(reloaded.Id, existingIngress.Id);
  assert.equal(reloaded.State.StartedAt, existingIngress.State.StartedAt);
  assert.deepEqual(reloaded.Mounts, existingIngress.Mounts);
  assert.equal(checked(firebotProbe), '204');
  const mounts = (name) => inspect(name).Mounts.filter(mount => mount.Type === 'bind');
  assert.deepEqual(mounts(ingress).map(mount => mount.Destination).sort(), [
    '/etc/nginx/conf.d', '/etc/nginx/tls',
  ]);
  assert.deepEqual(mounts(web).map(mount => mount.Destination).sort(), [
    '/etc/nginx/baobei-proxy.conf', '/etc/nginx/conf.d/default.conf', '/var/www/acme',
  ]);
  assert.equal(mounts(web).find(mount => mount.Destination === '/var/www/acme').RW, false);
  checked(['exec', web, 'test', '!', '-e', '/etc/nginx/tls/baobei/privkey.pem']);
  const write = docker(['exec', '-u', '0', web, 'touch', '/var/www/acme/forbidden']);
  assert.notEqual(write.status, 0);
  assert.match(write.stderr, /read-only file system/i);
  assert.equal(mounts(api).length, 0);
  assert.equal(mounts(pg).length, 0);

  const token = 'synthetic_token-' + suffix;
  const keyAuthorization = token + '.synthetic-thumbprint';
  assert.equal(request(probe, 'http', prefix + token).status, 404);
  // Simulate the host ACME process writing a token after the two Nginx containers have started.
  checked([
    'run', '--pull', 'never', '--rm', '--network', 'none', ...bind(webroot, '/writer', false),
    '-e', 'TOKEN=' + token, '-e', 'KEY_AUTHORIZATION=' + keyAuthorization,
    '--entrypoint', 'sh', tools, '-ec',
    'umask 077; (umask ugo+rx && printf "%s" "$KEY_AUTHORIZATION" >"/writer/.well-known/acme-challenge/$TOKEN"); ln -s /usr/share/nginx/html/index.html /writer/.well-known/acme-challenge/outside-link',
  ]);
  assert.equal(checked(['exec', '-u', 'nginx', web, 'cat', '/var/www/acme' + prefix + token]), keyAuthorization);
  checked(['exec', web, 'test', '-L', '/var/www/acme' + prefix + 'outside-link']);
  assert.equal(request(probe, 'http', prefix + 'outside-link').status, 404);
  const found = request(probe, 'http', prefix + token + '?fixture=1');
  assert.equal(found.status, 200, JSON.stringify(found.headers));
  assert.equal(found.body, keyAuthorization);
  assert.match(found.headers['content-type'], /^text\/plain\b/);
  assert.equal(found.headers['cache-control'], 'no-store');
  assert.equal(found.headers['x-content-type-options'], 'nosniff');
  assert.match(found.headers['content-security-policy'], /default-src 'self'/);
  assert.equal(request(probe, 'http', prefix + token, 'HEAD').status, 200);
  const post = request(probe, 'http', prefix + token, 'POST');
  assert.equal(post.status, 405);
  assert.equal(post.headers.allow, 'GET, HEAD');
  for (const path of [prefix, prefix + 'directory', prefix + 'directory/', prefix + 'privkey.pem',
    prefix + 'directory/../' + token, prefix + '%2e%2e/outside']) {
    const response = request(probe, 'http', path);
    assert([308, 404].includes(response.status), path);
    assert(!response.body.includes(keyAuthorization) && !response.body.includes('synthetic-outside-challenge'));
  }
  assert.equal(request(probe, 'https', prefix + token).body, keyAuthorization);
  const redirect = request(probe, 'http', '/projects/synthetic?keep=1');
  assert.equal(redirect.status, 308);
  assert.equal(redirect.headers.location, 'https://' + domain + '/projects/synthetic?keep=1');
  const noSlash = request(probe, 'http', prefix.slice(0, -1));
  assert.equal(noSlash.status, 308);
  assert.equal(noSlash.headers.location, 'https://' + domain + prefix.slice(0, -1));

  assert.equal(request(probe, 'https', '/api/v1/me').status, 401);
  assert.equal(request(probe, 'https', '/auth/login').status, 503);
  assert.equal(request(probe, 'https', '/auth/callback?code=synthetic').status, 400);
  const config = request(probe, 'https', '/auth/config');
  assert.equal(config.status, 200);
  assert.deepEqual(JSON.parse(config.body), {development: false, oidc: false});
  const health = request(probe, 'https', '/health/ready');
  assert.equal(health.status, 200);
  assert.deepEqual(JSON.parse(health.body), {ok: true});
  const spa = request(probe, 'https', '/projects/synthetic');
  assert.equal(spa.status, 200);
  assert.match(spa.body, /项目报备中心/);
  console.log('HTTP01_BRIDGE=PASS CROSS_CONTAINER_HTTP01=PASS WEB_API_REGRESSION=PASS');
  console.log('SHARED_INGRESS_RECREATE_REQUIRED_BY_BAOBEI=NO');
  console.log('INGRESS_NEW_ACME_VOLUME_REQUIRED=NO BAOBEI_WEB_ACME_MOUNT_READ_ONLY=YES BAOBEI_WEB_NO_TLS_PRIVATE_KEY_MOUNT=YES');
} catch (error) {
  console.error('HTTP01_BRIDGE=BLOCKED');
  throw error;
} finally {
  for (const name of containers.reverse()) docker(['rm', '-f', name]);
  for (const name of networks.reverse()) docker(['network', 'rm', name]);
  const resolved = realpathSync(scratch);
  assert.equal(dirname(resolved), realpathSync(scratchRoot), 'Fixture cleanup escaped scratch root');
  rmSync(resolved, { recursive: true, force: true });
}
