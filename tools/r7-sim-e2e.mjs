// R7 SIM-E2E gate: real Mosquitto (the pinned production image), real PostgreSQL 17 and the
// unchanged Edge Agent runner + cloud runtime loop + BFF, inside the existing `integration` CI job.
// Every required test must PASS; a SKIP, a missing test or an extra test fails the gate.
//
//   node tools/r7-sim-e2e.mjs            CI default (20 devices hold 90 s)
//   R7_SIM_SOAK_MINUTES=60 node tools/r7-sim-e2e.mjs   60-minute soak (manual / server)
//
// Server use (no Go toolchain needed): R7_SIM_BUILD=docker compiles the test binary inside the
// pinned Go image of deploy/Dockerfile.backend (optional R7_SIM_GOPROXY, e.g. https://goproxy.cn,direct;
// go.sum and the checksum DB stay in force). The run is isolated: its own throw-away PostgreSQL
// container (tmpfs) and per-test Mosquitto containers on 127.0.0.1 random ports, labelled and
// removed afterwards — the Crane database, the shared broker and other platforms are never used.
// R7_SIM_ONLY=TestR7Sim20DevicesSoak runs just the soak (the gate then requires exactly that test).
import { spawnSync } from 'node:child_process';
import { mkdirSync, writeFileSync, chmodSync, readFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { gateProblems, runPattern } from './r2-db-gate.mjs';

export const REQUIRED = [
  'TestR7Sim1DeviceFullLoop',
  'TestR7Sim3DevicesIndependent',
  'TestR7SimSpoolAckLossNoDuplicates',
  'TestR7SimMQTTAbuseAndOrdering',
  'TestR7Sim5DevicesRolesIsolationRevocation',
  'TestR7Sim20DevicesSoak',
  'TestR7IsolatedRestoreDrill',
  'TestR8SimCertScriptIssuesUsableIdentity',
];

function run(command, args, extra = {}, capture = false) {
  const r = spawnSync(command, args, { encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit', env: { ...process.env, ...extra } });
  if (r.error || r.status !== 0) {
    if (capture) { process.stdout.write(r.stdout || ''); process.stderr.write(r.stderr || ''); }
    throw Error(`R7 SIM-E2E command failed: ${command} ${args.slice(0, 3).join(' ')}`);
  }
  return r.stdout || '';
}

if (import.meta.main) {
  const root = resolve('deploy/compose/runtime/r7-sim-e2e');
  mkdirSync(root, { recursive: true, mode: 0o700 }); chmodSync(root, 0o700);
  const images = JSON.parse(readFileSync('deploy/production/images.json', 'utf8'));
  const password = randomBytes(24).toString('hex');
  const name = `crane-r7-sim-${process.pid}`;
  const pg = `${name}-pg`;
  const binary = join(root, 'app.test');
  const sha = spawnSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).stdout.trim();
  const dirty = spawnSync('git', ['status', '--porcelain', '--untracked-files=no'], { encoding: 'utf8' }).stdout.trim() !== '';
  if (process.env.R7_SIM_BUILD === 'docker') {
    const goImage = /^FROM (golang:\S+@sha256:[a-f0-9]{64})/m.exec(readFileSync('deploy/Dockerfile.backend', 'utf8'))?.[1];
    if (!goImage) throw Error('pinned Go image not found in deploy/Dockerfile.backend');
    const proxy = process.env.R7_SIM_GOPROXY ?? '';
    if (proxy && !/^(https:\/\/[A-Za-z0-9.:/_-]+|direct|off)(,(https:\/\/[A-Za-z0-9.:/_-]+|direct|off))*$/.test(proxy)) throw Error('R7_SIM_GOPROXY must list https URLs, direct or off');
    const cache = resolve('deploy/compose/runtime/r7-sim-e2e/gocache'); mkdirSync(cache, { recursive: true, mode: 0o700 });
    run('docker', ['run', '--rm', '--user', `${process.getuid()}:${process.getgid()}`, '-v', `${process.cwd()}:/src`, '-w', '/src', '-e', 'CGO_ENABLED=0', '-e', 'GOFLAGS=-buildvcs=false',
      '-e', 'GOCACHE=/src/deploy/compose/runtime/r7-sim-e2e/gocache/build', '-e', 'GOMODCACHE=/src/deploy/compose/runtime/r7-sim-e2e/gocache/mod', '-e', 'HOME=/tmp',
      ...(proxy ? ['-e', `GOPROXY=${proxy}`] : []), goImage, 'go', 'test', '-c', '-o', 'deploy/compose/runtime/r7-sim-e2e/app.test', './services/cloud-api/internal/app']);
  } else {
    run('go', ['test', '-c', '-o', binary, './services/cloud-api/internal/app']);
  }
  console.log(`R7_SIM_SOURCE sha=${sha} dirty=${dirty} soak=${process.env.R7_SIM_SOAK_MINUTES ? process.env.R7_SIM_SOAK_MINUTES + 'min' : (process.env.R7_SIM_SOAK_SECONDS ?? '90') + 's'}`);
  const evidence = resolve('test-results-r7/sim-evidence.jsonl');
  mkdirSync(resolve('test-results-r7'), { recursive: true });
  try {
    run('docker', ['pull', '-q', images.mosquitto], {}, true);
    run('docker', ['run', '-d', '--rm', '--name', pg, '-e', `POSTGRES_PASSWORD=${password}`, '-e', 'POSTGRES_USER=a1test', '-e', 'POSTGRES_DB=a1test', '-p', '127.0.0.1::5432', '--tmpfs', '/var/lib/postgresql/data', images.postgres], {}, true);
    const port = run('docker', ['port', pg, '5432/tcp'], {}, true).trim().split('\n')[0].split(':').pop();
    for (let i = 0; i < 60; i++) {
      const r = spawnSync('docker', ['exec', pg, 'pg_isready', '-U', 'a1test', '-d', 'a1test']);
      if (r.status === 0) break;
      spawnSync('sleep', ['1']);
    }
    // The test starts its own broker per test through this wrapper: the pinned Mosquitto image with
    // the test-generated config/certs mounted read-only (no host Mosquitto package required).
    const wrapper = join(root, 'mosquitto');
    writeFileSync(wrapper, `#!/bin/sh
# R7 SIM-E2E: run the pinned Mosquitto image with the generated config (arg: -c <conf>).
conf="$2"; dir="$(dirname "$conf")"
exec docker run --rm --init --label r7-sim-e2e=${name} --network host --user "$(id -u):$(id -g)" -v "$dir:$dir" ${images.mosquitto} mosquitto -c "$conf"
`, { mode: 0o700 });
    const only = process.env.R7_SIM_ONLY ? process.env.R7_SIM_ONLY.split(',') : null;
    if (only && only.some((t) => !REQUIRED.includes(t))) throw Error('R7_SIM_ONLY must name required tests');
    const selected = only ?? REQUIRED;
    const output = run(binary, ['-test.v', '-test.count=1', '-test.timeout=90m', '-test.run=' + runPattern(selected)], {
      R7_SIM_E2E: '1', MOSQUITTO_BIN: wrapper, R7_SIM_EVIDENCE: evidence,
      // Restore drill: pg_dump / pg_restore of the server's own version, inside the DB container.
      R7_RESTORE_DRILL: '1', R7_PG_EXEC: `docker exec -i -e PGPASSWORD=${password} ${pg}`,
      A1_TEST_DATABASE_URL: `postgres://a1test:${password}@127.0.0.1:${port}/a1test?sslmode=disable`,
    }, true);
    process.stdout.write(output);
    const problems = gateProblems(output, selected, { exact: true });
    if (problems.length) throw Error('R7 SIM-E2E gate failed: ' + problems.join('; '));
    for (const t of selected) console.log('R7_SIM_E2E_REQUIRED_PASS ' + t);
    if (only) { console.log(`R7_SIM_E2E_SUBSET=PASS tests=${selected.join(',')} sha=${sha} evidence=${evidence}`); }
    else {
      console.log('R7_SIM_E2E=PASS MOSQUITTO=REAL POSTGRES=REAL EDGE_AGENT=REAL BFF=REAL');
      console.log('R7_ISOLATED_RESTORE_DRILL=PASS');
    }
  } finally {
    spawnSync('docker', ['rm', '-f', pg], { stdio: 'ignore' });
    const left = spawnSync('docker', ['ps', '-aq', '--filter', `label=r7-sim-e2e=${name}`], { encoding: 'utf8' }).stdout.trim().split('\n').filter(Boolean);
    if (left.length) spawnSync('docker', ['rm', '-f', ...left], { stdio: 'ignore' });
  }
}
