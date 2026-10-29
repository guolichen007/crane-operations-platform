import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';

export const frontendNetwork = 'u-dao-crane-frontend';
export const ingressTemplate = 'deploy/ingress/crane.youdaozd.com.conf.example';
export function validateIngress(text = readFileSync(ingressTemplate, 'utf8'), common = readFileSync('deploy/ingress/crane-http-common.conf.example', 'utf8')) {
  const active = text.replace(/#.*$/gm, '');
  assert.doesNotMatch(active, /proxy_pass\s+https?:\/\/(?:127\.0\.0\.1|localhost|\[::1\])/i, 'Production ingress cannot proxy to container loopback');
  assert.match(active, /resolver\s+127\.0\.0\.11\s+valid=5s\s+ipv6=off\s*;/);
  assert.match(active, /set\s+\$crane_web\s+web-console:8080\s*;/);
  assert.match(active, /set\s+\$crane_keycloak\s+keycloak:8080\s*;/);
  const passes = [...active.matchAll(/proxy_pass\s+([^;]+);/g)].map(match => match[1]);
  assert.equal(passes.length, 3);
  assert.ok(passes.every(value => ['http://$crane_web', 'http://$crane_keycloak'].includes(value)), 'All upstreams must defer DNS with variables');
  assert.match(active, /location\s+\/\s*\{\s*proxy_pass\s+http:\/\/\$crane_web;/);
  assert.match(active, /location\s+~\s+\^\/\(realms\|resources\)\/\s*\{[^}]*proxy_pass\s+http:\/\/\$crane_keycloak;/);
  assert.match(active, /location\s+=\s+\/auth\/callback\s*\{\s*access_log\s+off;[^}]*proxy_pass\s+http:\/\/\$crane_web;/);
  for (const path of ['= /admin', '/admin/', '/health', '/metrics']) assert.ok(active.includes(`location ${path} { deny all; }`), 'Public administration/health/metrics must be denied');
  assert.match(common, /log_format crane_safe '\$remote_addr \$request_method \$uri \$status \$body_bytes_sent';/);
  assert.doesNotMatch(common, /\$(?:request_uri|args|query_string|request|http_cookie|http_referer)\b/, 'Ingress access logs cannot contain code/state or credentials');
  return true;
}

// Routing comes from the reviewed production template, never a separate test implementation.
export function fixtureIngress(port) {
  validateIngress();
  const server = readFileSync(ingressTemplate, 'utf8')
    .replace('listen 443 ssl;', `listen ${port} ssl;`)
    .replace('server_name crane.youdaozd.com;', 'server_name crane.test;')
    .replace('/REVIEW_REQUIRED/crane/fullchain.pem', '/fixture/ingress/ingress.crt')
    .replace('/REVIEW_REQUIRED/crane/privkey.pem', '/fixture/ingress/ingress.key')
    .replaceAll('$host;', '$http_host;'); // Anonymous HTTPS fixture uses a random port.
  return `events {}\nhttp {\n${readFileSync('deploy/ingress/crane-http-common.conf.example', 'utf8')}\n${server}\n}\n`;
}

// No caller-supplied name: the fixture can never adopt the real shared network.
export function createFixtureNetwork(execute) {
  const owner = randomUUID().replaceAll('-', ''), name = `crane-a1-prod-test-${owner}`;
  const id = execute(['network', 'create', '--label', `crane.a1.fixture=${owner}`, name]).trim();
  assert.match(id, /^[a-f0-9]{64}$/, 'Docker must return the newly created network ID');
  return { name, id, remove() {
    const [network] = JSON.parse(execute(['network', 'inspect', id]));
    assert.equal(network.Id, id);
    assert.equal(network.Name, name);
    assert.equal(network.Labels?.['crane.a1.fixture'], owner, 'Only the owned anonymous network can be deleted');
    execute(['network', 'rm', id]);
  } };
}
