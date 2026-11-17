// R7 SIM-E2E gate: real Mosquitto (the pinned production image), real PostgreSQL 17 and the
// unchanged Edge Agent runner + cloud runtime loop + BFF, inside the existing `integration` CI job.
// Every required test must PASS; a SKIP, a missing test or an extra test fails the gate.
//
//   node tools/r7-sim-e2e.mjs            CI default (20 devices hold 90 s)
//   R7_SIM_SOAK_MINUTES=60 node tools/r7-sim-e2e.mjs   60-minute soak (manual / scheduled)
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
  run('go', ['test', '-c', '-o', binary, './services/cloud-api/internal/app']);
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
    const output = run(binary, ['-test.v', '-test.count=1', '-test.timeout=90m', '-test.run=' + runPattern(REQUIRED)], {
      R7_SIM_E2E: '1', MOSQUITTO_BIN: wrapper, R7_SIM_EVIDENCE: evidence,
      // Restore drill: pg_dump / pg_restore of the server's own version, inside the DB container.
      R7_RESTORE_DRILL: '1', R7_PG_EXEC: `docker exec -i -e PGPASSWORD=${password} ${pg}`,
      A1_TEST_DATABASE_URL: `postgres://a1test:${password}@127.0.0.1:${port}/a1test?sslmode=disable`,
    }, true);
    process.stdout.write(output);
    const problems = gateProblems(output, REQUIRED, { exact: true });
    if (problems.length) throw Error('R7 SIM-E2E gate failed: ' + problems.join('; '));
    for (const t of REQUIRED) console.log('R7_SIM_E2E_REQUIRED_PASS ' + t);
    console.log('R7_SIM_E2E=PASS MOSQUITTO=REAL POSTGRES=REAL EDGE_AGENT=REAL BFF=REAL');
    console.log('R7_ISOLATED_RESTORE_DRILL=PASS');
  } finally {
    spawnSync('docker', ['rm', '-f', pg], { stdio: 'ignore' });
    const left = spawnSync('docker', ['ps', '-aq', '--filter', `label=r7-sim-e2e=${name}`], { encoding: 'utf8' }).stdout.trim().split('\n').filter(Boolean);
    if (left.length) spawnSync('docker', ['rm', '-f', ...left], { stdio: 'ignore' });
  }
}
