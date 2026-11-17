// R8 server-side SIM tooling: openssl-only certificate issuance (SIM/TEST only, CA key never in the
// simulator directory, no overwrite) and simulator control (SIMULATION fleet only, local image, no pull).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, existsSync, rmSync, chmodSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const sh = (args, cwd) => spawnSync('bash', args, { cwd, encoding: 'utf8' });

test('sim-cert.sh: issues a CN=id client cert from the CA, refuses REAL ids, overwrite and CA-dir output', (t) => {
  const d = mkdtempSync(join(tmpdir(), 'crane-simcert-')); t.after(() => rmSync(d, { recursive: true, force: true }));
  mkdirSync(join(d, 'ca'));
  const o = (...a) => assert.equal(spawnSync('openssl', a, { cwd: d }).status, 0, a.join(' '));
  o('ecparam', '-name', 'prime256v1', '-genkey', '-noout', '-out', 'ca/k.pem');
  o('pkcs8', '-topk8', '-nocrypt', '-in', 'ca/k.pem', '-out', 'ca/ca.key'); chmodSync(join(d, 'ca/ca.key'), 0o600);
  o('req', '-x509', '-new', '-key', 'ca/ca.key', '-subj', '/CN=Crane Test CA', '-days', '400', '-out', 'ca/ca.crt', '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign');
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

function rsaCA(d, { days = 3650, cn = 'Crane Site MQTT CA' } = {}) {
  mkdirSync(join(d, 'ca'), { recursive: true });
  const o = (...a) => assert.equal(spawnSync('openssl', a, { cwd: d }).status, 0, a.join(' '));
  o('genrsa', '-out', 'ca/ca.key', '2048'); chmodSync(join(d, 'ca/ca.key'), 0o600);
  o('req', '-x509', '-new', '-key', 'ca/ca.key', '-subj', `/CN=${cn}`, '-days', String(days), '-out', 'ca/ca.crt', '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign');
}

test('sim-cert.sh: existing RSA CA issues a matching clientAuth leaf; CA lifetime, key ownership and pairing are enforced', (t) => {
  const d = mkdtempSync(join(tmpdir(), 'crane-simcert-rsa-')); t.after(() => rmSync(d, { recursive: true, force: true }));
  rsaCA(d);
  const script = join(process.cwd(), 'deploy/scripts/sim-cert.sh');
  const caBefore = readFileSync(join(d, 'ca/ca.crt'));
  let r = sh([script, 'issue', '--ca-dir', 'ca', '--out', 'sim', '--id', 'SIM-UAT-0001', '--days', '30'], d);
  assert.equal(r.status, 0, r.stderr);
  assert.match(r.stdout, /ca_alg=rsaEncryption/);
  assert.deepEqual(readFileSync(join(d, 'ca/ca.crt')), caBefore, 'CA is never re-created');
  r = sh([script, 'verify', '--ca-dir', 'ca', '--out', 'sim', '--id', 'SIM-UAT-0001'], d);
  assert.equal(r.status, 0, r.stderr);
  // second identity into the same directory reuses the identical ca.crt
  assert.equal(sh([script, 'issue', '--ca-dir', 'ca', '--out', 'sim', '--id', 'SIM-UAT-0002', '--days', '30'], d).status, 0);
  // key / certificate mismatch is detected by verify
  const k1 = readFileSync(join(d, 'sim/SIM-UAT-0001.key')); const k2 = readFileSync(join(d, 'sim/SIM-UAT-0002.key'));
  writeFileSync(join(d, 'sim/SIM-UAT-0001.key'), k2, { mode: 0o600 });
  r = sh([script, 'verify', '--ca-dir', 'ca', '--out', 'sim', '--id', 'SIM-UAT-0001'], d);
  assert.notEqual(r.status, 0); assert.match(r.stderr, /does not match/);
  writeFileSync(join(d, 'sim/SIM-UAT-0001.key'), k1, { mode: 0o600 });
  // a CA whose key is not the CA certificate's key is refused
  const other = mkdtempSync(join(tmpdir(), 'crane-simcert-x-')); t.after(() => rmSync(other, { recursive: true, force: true }));
  rsaCA(other); writeFileSync(join(d, 'ca2.key'), readFileSync(join(other, 'ca/ca.key')));
  mkdirSync(join(d, 'ca2')); writeFileSync(join(d, 'ca2/ca.crt'), caBefore); writeFileSync(join(d, 'ca2/ca.key'), readFileSync(join(other, 'ca/ca.key')), { mode: 0o600 });
  r = sh([script, 'issue', '--ca-dir', 'ca2', '--out', 'sim2', '--id', 'SIM-UAT-0003'], d);
  assert.notEqual(r.status, 0); assert.match(r.stderr, /does not belong/);
  // the CA must outlive the leaf
  const short = mkdtempSync(join(tmpdir(), 'crane-simcert-s-')); t.after(() => rmSync(short, { recursive: true, force: true }));
  rsaCA(short, { days: 10 });
  r = sh([script, 'issue', '--ca-dir', 'ca', '--out', 'sim3', '--id', 'SIM-UAT-0004', '--days', '30'], short);
  assert.notEqual(r.status, 0); assert.match(r.stderr, /expires before/);
  // --owner needs numeric UID:GID and root
  r = sh([script, 'issue', '--ca-dir', 'ca', '--out', 'sim4', '--id', 'SIM-UAT-0005', '--owner', 'app'], d);
  assert.notEqual(r.status, 0); assert.match(r.stderr, /numeric UID:GID/);
  if (process.getuid() !== 0) {
    r = sh([script, 'issue', '--ca-dir', 'ca', '--out', 'sim4', '--id', 'SIM-UAT-0005', '--owner', '10001:10001'], d);
    assert.notEqual(r.status, 0); assert.match(r.stderr, /requires root/);
  }
});

test('sim-control.sh: SIMULATION-only fleet, local image by SHA, never pull, CA key refused, only device-simulator', () => {
  const s = readFileSync('deploy/scripts/sim-control.sh', 'utf8');
  for (const needle of ['--pull never', '--no-deps', 'crane-local/device-simulator:$sha', 'sourceMode") != "SIMULATION"', 'ca.key', 'org.opencontainers.image.revision', '--profile simulation', 'docker-compose.prod.simulation.yml',
    'active-override.yml', '-f "$active"', 'CRANE_FLEET_FILE', 'registered identically in the active cloud Fleet', '1..20 identities', 'SIM_UID=10001', '$SIM_UID 700', '$SIM_UID 600', 'does not match', '-purpose sslclient', 'crane-simulation-spool'])
    assert.ok(s.includes(needle), needle);
  for (const f of ['deploy/scripts/sim-control.sh', 'deploy/scripts/sim-cert.sh'])
    assert.match(spawnSync('git', ['ls-files', '-s', f], { encoding: 'utf8' }).stdout, /^100755 /, f + ' must be committed executable');
  assert.match(readFileSync('deploy/Dockerfile.backend', 'utf8'), /install -d -o 10001 -g 10001 -m 0700 \/spool/);
  assert.doesNotMatch(s, /docker (compose .*)?(down|rm -f)|\bpull\b(?! never)/);
  assert.match(readFileSync('deploy/compose/docker-compose.prod.simulation.yml', 'utf8'), /\/certs:ro/);
});
