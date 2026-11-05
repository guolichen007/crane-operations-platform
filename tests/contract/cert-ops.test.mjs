import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import YAML from 'yaml';
import { validateIngress } from '../../tools/production-topology.mjs';
const read = path => readFileSync(path, 'utf8');
function http01(text) {
  const active = text.replace(/#.*$/gm, '');
  assert.match(active, /server_name crane\.youdaozd\.com;/);
  assert.doesNotMatch(active, /firebot|listen 443/);
  assert.match(active, /location \^~ \/\.well-known\/acme-challenge\/\s*\{\s*root \/var\/www\/acme;\s*default_type text\/plain;\s*try_files \$uri =404;\s*access_log off;\s*add_header Cache-Control "no-store" always;\s*\}/);
  assert.match(active, /location \/ \{ return 301 https:\/\/crane\.youdaozd\.com\$request_uri; \}/);
  assert.equal((active.match(/return /g) || []).length, 1);
  assert.equal((active.match(/location /g) || []).length, 2);
  assert.ok(active.indexOf('acme-challenge') < active.indexOf('return 301'));
}
test('Crane HTTP-01 route precedes location redirect, never changes HTTPS/BFF or Firebot routing', () => {
  const text = read('deploy/ingress/crane-http01.conf.example'); http01(text); validateIngress();
  for (const changed of [text.replace('access_log off;', 'access_log on;'), text.replace('location ^~', 'location'), text.replace('try_files $uri =404;', ''), text.replace('location / { return 301', 'return 301')]) assert.throws(() => http01(changed));
  assert.match(read('deploy/ingress/crane-acme-webroot.compose.example.yml'), /\/srv\/u-dao\/platform\/ingress\/acme-webroot:\/var\/www\/acme:ro/);
});
test('renewal is root-only, locks, fixed LE/RSA, no force/unsafe TLS/restart/cron install', () => {
  const script = read('deploy/scripts/cert-renew.sh');
  for (const item of ['set -Eeuo pipefail', 'umask 077', 'flock -n 9', '--server letsencrypt', '--keylength 2048', '--install-cert', '--cron', '--bootstrap', '--adopt', '--run', '--check', '-verify_hostname', '-verify_return_error', 'CERT_BOOTSTRAP=READY_FOR_ADOPTION', 'result CRITICAL']) assert.ok(script.includes(item), item);
  assert.doesNotMatch(script, /--force|curl -k|InsecureSkipVerify|docker (?:restart|compose)|nginx -s stop|dns_tencent/);
  for (const call of script.matchAll(/\bcurl\s+([^\n]+)/g)) assert.ok(call[1].startsWith('--disable '), 'curl must ignore ambient configuration');
  const unit = read('deploy/systemd/u-dao-crane-cert-renew.service');
  for (const item of ['Type=oneshot', 'User=root', 'UMask=0077', 'ExecStart=/usr/local/sbin/u-dao-crane-cert-renew --run', 'ProtectSystem=strict', 'AF_UNIX AF_INET AF_INET6']) assert.ok(unit.includes(item));
  assert.doesNotMatch(unit, /PrivateNetwork=true/);
  const timer = read('deploy/systemd/u-dao-crane-cert-renew.timer');
  for (const item of ['OnCalendar=*-*-* 00,06,12,18:00:00', 'RandomizedDelaySec=30m', 'Persistent=true']) assert.ok(timer.includes(item));
});
test('installer checks signed pinned upstream before installation and cannot issue/reload/enable', () => {
  const script = read('deploy/scripts/provision-acme.sh');
  assert.ok(script.includes('https://github.com/acmesh-official/acme.sh.git'));
  for (const item of ['VERSION=3.1.6','807da6498377ee5e0cf43a78091f46f12dc59a89','c6e7807f8e2573bf8b02b71da186b4022afafa56','gpg.ssh.allowedSignersFile=', 'verify-tag "$VERSION"', 'sha256sum']) assert.ok(script.includes(item));
  assert.ok(script.indexOf('verify-tag') < script.indexOf('git -C "$temp/source" archive'));
  assert.ok(script.indexOf('systemctl is-active u-dao-crane-cert-renew.service') < script.indexOf('clone --depth'));
  assert.ok(script.indexOf('then renewal_lock; fi') < script.indexOf('clone --depth'));
  assert.ok(script.includes('flock -n 9'));
  assert.ok(script.includes('deploy/scripts/cert-state.py" /usr/local/libexec/u-dao-crane-cert-state'));
  assert.doesNotMatch(script, /--issue|--install-cert|--cron|--auto-upgrade|systemctl enable|nginx -s reload|curl.*\|.*sh/);
});
// a1SecurityAudit extracts the checks that must hold on the isolated a1-security
// Job. Scoping is done on the parsed YAML job (not a string slice between two
// sibling keys), so a neighbouring Job — e.g. b4-business-acceptance with a legal
// `if: always()` — can never leak into the audit.
function a1SecurityAudit(job) {
  const steps = (job && Array.isArray(job.steps) && job.steps) || [];
  const runs = steps.map((step) => (step && typeof step.run === 'string' ? step.run : '')).join('\n');
  const required = ['bash -n deploy/scripts/cert-renew.sh', 'bash -n deploy/scripts/provision-acme.sh', 'sudo bash tests/ops/verify-cert-units.sh', 'sudo env PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests/ops -v'];
  const missing = required.filter((command) => !runs.includes(command));
  const dangerous = [];
  if (job && typeof job === 'object' && 'if' in job) dangerous.push('job-level if: ' + job.if);
  for (const step of steps) {
    if (step && typeof step === 'object') {
      if ('if' in step) dangerous.push('if: ' + step.if);
      if ('continue-on-error' in step) dangerous.push('continue-on-error');
      if (typeof step.run === 'string' && step.run.includes('|| true')) dangerous.push('|| true');
    }
  }
  return { missing, dangerous };
}

test('required a1-security job runs shell syntax, real systemd parser and Linux interruption fixtures', () => {
  const job = YAML.parse(read('.github/workflows/ci.yml')).jobs['a1-security'];
  assert.ok(job, 'a1-security job must exist');
  assert.deepEqual(a1SecurityAudit(job), { missing: [], dangerous: [] });
  const verify = read('tests/ops/verify-cert-units.sh');
  assert.match(verify, /systemd-analyze --man=no verify/);
  assert.doesNotMatch(verify, /systemctl|daemon-reload/);
});

test('相邻 Job 的合法 if 不影响 a1-security 检查范围', () => {
  const workflow = YAML.parse(`
jobs:
  a1-security:
    runs-on: ubuntu-latest
    steps:
      - run: bash -n deploy/scripts/cert-renew.sh
      - run: bash -n deploy/scripts/provision-acme.sh
      - run: sudo bash tests/ops/verify-cert-units.sh
      - run: sudo env PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests/ops -v
  b4-business-acceptance:
    runs-on: ubuntu-latest
    steps:
      - run: echo neighbour
      - if: always()
        run: echo archive evidence
`);
  assert.deepEqual(a1SecurityAudit(workflow.jobs['a1-security']), { missing: [], dangerous: [] });
});

test('a1-security 自身加入危险跳过配置时必须失败', () => {
  const workflow = YAML.parse(`
jobs:
  a1-security:
    runs-on: ubuntu-latest
    steps:
      - run: bash -n deploy/scripts/cert-renew.sh
      - run: bash -n deploy/scripts/provision-acme.sh
      - run: sudo bash tests/ops/verify-cert-units.sh
      - run: sudo env PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests/ops -v
      - if: failure()
        run: echo silently skipped
`);
  assert.ok(a1SecurityAudit(workflow.jobs['a1-security']).dangerous.length > 0, 'dangerous skip condition must be detected');
});

test('a1-security Job 级跳过配置必须失败', () => {
  const workflow = YAML.parse(`
jobs:
  a1-security:
    runs-on: ubuntu-latest
    if: always()
    steps:
      - run: bash -n deploy/scripts/cert-renew.sh
      - run: bash -n deploy/scripts/provision-acme.sh
      - run: sudo bash tests/ops/verify-cert-units.sh
      - run: sudo env PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests/ops -v
`);
  assert.ok(a1SecurityAudit(workflow.jobs['a1-security']).dangerous.length > 0, 'job-level skip condition must be detected');
});
