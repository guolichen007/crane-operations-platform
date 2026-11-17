// R8 server-side SIM tooling: openssl-only certificate issuance (SIM/TEST only, CA key never in the
// simulator directory, no overwrite) and simulator control (SIMULATION fleet only, local image, no pull).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, existsSync, rmSync, chmodSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const sh = (args, cwd) => spawnSync('bash', args, { cwd, encoding: 'utf8' });

test('sim-cert.sh: issues a CN=id client cert from the CA, refuses REAL ids, overwrite and CA-dir output', (t) => {
  const d = mkdtempSync(join(tmpdir(), 'crane-simcert-')); t.after(() => rmSync(d, { recursive: true, force: true }));
  mkdirSync(join(d, 'ca'));
  const o = (...a) => assert.equal(spawnSync('openssl', a, { cwd: d }).status, 0, a.join(' '));
  o('ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', 'ca/k.pem');
  o('pkcs8', '-topk8', '-nocrypt', '-in', 'ca/k.pem', '-out', 'ca/ca.key'); chmodSync(join(d, 'ca/ca.key'), 0o600);
  o('req', '-x509', '-new', '-key', 'ca/ca.key', '-subj', '/CN=Crane Test CA', '-days', '2', '-out', 'ca/ca.crt');
  const script = join(process.cwd(), 'deploy/scripts/sim-cert.sh');
  let r = sh([script, 'issue', '--ca-dir', 'ca', '--out', 'sim', '--id', 'SIM-T-1'], d);
  assert.equal(r.status, 0, r.stderr);
  assert.ok(existsSync(join(d, 'sim/SIM-T-1.crt')) && !existsSync(join(d, 'sim/ca.key')));
  assert.equal(sh([script, 'verify', '--ca-dir', 'ca', '--out', 'sim', '--id', 'SIM-T-1'], d).status, 0);
  const subject = spawnSync('openssl', ['x509', '-in', 'sim/SIM-T-1.crt', '-noout', '-subject', '-ext', 'extendedKeyUsage'], { cwd: d, encoding: 'utf8' }).stdout;
  assert.match(subject, /CN ?= ?SIM-T-1/); assert.match(subject, /TLS Web Client Authentication/);
  for (const [args, msg] of [[['--id', 'SIM-T-1', '--out', 'sim'], /never overwritten/], [['--id', 'EDGE-1', '--out', 'x'], /REAL identities/], [['--id', 'SIM-T-2', '--out', 'ca/x'], /CA directory/], [['--id', 'SIM-T-3', '--out', 'y', '--days', '400'], /days/]]) {
    r = sh([script, 'issue', '--ca-dir', 'ca', ...args], d);
    assert.notEqual(r.status, 0); assert.match(r.stderr, msg);
  }
});

test('sim-control.sh: SIMULATION-only fleet, local image by SHA, never pull, CA key refused, only device-simulator', () => {
  const s = readFileSync('deploy/scripts/sim-control.sh', 'utf8');
  for (const needle of ['--pull never', '--no-deps', 'crane-local/device-simulator:$sha', 'sourceMode") != "SIMULATION"', 'ca.key', 'org.opencontainers.image.revision', '--profile simulation', 'docker-compose.prod.simulation.yml'])
    assert.ok(s.includes(needle), needle);
  assert.doesNotMatch(s, /docker (compose .*)?(down|rm -f)|\bpull\b(?! never)/);
  assert.match(readFileSync('deploy/compose/docker-compose.prod.simulation.yml', 'utf8'), /\/certs:ro/);
});
