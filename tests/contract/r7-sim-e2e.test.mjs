// R7: the SIM-E2E gate runs inside the required `integration` job, exact-runs every scenario and
// refuses SKIP; the harness never fabricates REST/WebSocket responses.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import YAML from 'yaml';
import { REQUIRED } from '../../tools/r7-sim-e2e.mjs';

test('integration job runs the R7 SIM-E2E gate unconditionally', () => {
  const job = YAML.parse(readFileSync('.github/workflows/ci.yml', 'utf8')).jobs.integration;
  const step = job.steps.find((s) => (s.run || '').trim() === 'node tools/r7-sim-e2e.mjs');
  assert.ok(step, 'gate step missing');
  assert.equal(step.if, undefined, 'gate step must not be conditional');
  assert.equal(job.if, undefined);
});

test('gate requires all six scenarios, the restore drill and the SIM certificate script, exact, no skip', () => {
  assert.deepEqual(REQUIRED, ['TestR7Sim1DeviceFullLoop', 'TestR7Sim3DevicesIndependent', 'TestR7SimSpoolAckLossNoDuplicates', 'TestR7SimMQTTAbuseAndOrdering', 'TestR7Sim5DevicesRolesIsolationRevocation', 'TestR7Sim20DevicesSoak', 'TestR7IsolatedRestoreDrill', 'TestR8SimCertScriptIssuesUsableIdentity']);
  const src = readFileSync('tools/r7-sim-e2e.mjs', 'utf8');
  // The CI step sets no R7_SIM_ONLY, so the exact gate runs over all of REQUIRED; a manual subset
  // (server soak) may only name required tests and prints a SUBSET marker, never R7_SIM_E2E=PASS.
  assert.match(src, /const selected = only \?\? REQUIRED;/);
  assert.match(src, /gateProblems\(output, selected, \{ exact: true \}\)/);
  assert.match(src, /R7_SIM_ONLY must name required tests/);
  assert.match(src, /if \(only\) \{ console\.log\(`R7_SIM_E2E_SUBSET=PASS/);
  const job = YAML.parse(readFileSync('.github/workflows/ci.yml', 'utf8')).jobs.integration;
  assert.ok(!JSON.stringify(job).includes('R7_SIM_ONLY'), 'CI must not narrow the gate');
  assert.match(src, /images\.mosquitto/);
  assert.match(src, /images\.postgres/);
});

test('soak asserts are hard: SQL errors fail, sample count / duration / faults / recovery / P95 asserted', () => {
  const src = readFileSync('services/cloud-api/internal/app/r7_sim_e2e_test.go', 'utf8');
  const soak = src.slice(src.indexOf('func TestR7Sim20DevicesSoak'));
  assert.doesNotMatch(soak.slice(0, soak.indexOf('\n}\n')), /_ = e\.db\.QueryRow/, 'no ignored SQL error in the soak');
  for (const needle of ['t.Fatalf("soak query %s: %v"', 'held < duration', 'p.drops.Load()', 'p.accepts.Load()', 'cloud runtime restart did not happen', 'p95 > simLiveP95TargetMs', 'telemetry.SampleInterval', 'SOAK_RESOURCES', 'alarms != distinctAlarms'])
    assert.ok(soak.includes(needle), needle);
});

test('harness drives the real chain: no route interception or canned responses', () => {
  const files = ['services/cloud-api/internal/app/r7_sim_harness_test.go', 'services/cloud-api/internal/app/r7_sim_e2e_test.go'].map((f) => readFileSync(f, 'utf8')).join('\n');
  for (const needle of ['runner.RunDevice(', 'startRuntime(', 'h.register(mux)', 'websocket.Dial(', 'fleet.ACL(', 'runtimeidentity.Apply(']) assert.ok(files.includes(needle), needle);
  assert.doesNotMatch(files, /page\.route|httpmock|fulfill\(/);
  const tests = [...files.matchAll(/^func (TestR7Sim\w+)\(/gm)].map((m) => m[1]).sort();
  assert.deepEqual(tests, REQUIRED.filter((t) => t.startsWith('TestR7Sim')).sort());
  assert.match(readFileSync('services/cloud-api/internal/app/r7_restore_drill_test.go', 'utf8'), /crane_drill_/);
});

// R8: the test binary writes straight to a per-run log file (no pipe/buffer limit); the verdict uses
// the complete file plus the real exit code / signal / timeout; secrets are redacted.
import { runToFile, evaluateRun } from '../../tools/r7-sim-e2e.mjs';
import { mkdtempSync, rmSync, writeFileSync as wf, readFileSync as rf, existsSync as ex } from 'node:fs';
import { tmpdir } from 'node:os';
import { join as pj } from 'node:path';

function fakeBinary(t, body) {
  const d = mkdtempSync(pj(tmpdir(), 'r7-run-')); t.after(() => rmSync(d, { recursive: true, force: true }));
  const bin = pj(d, 'app.test'); wf(bin, '#!/bin/sh\n' + body + '\n', { mode: 0o700 });
  return { d, bin };
}
const PASSING = REQUIRED.map((n) => `echo "=== RUN   ${n}"; echo "--- PASS: ${n} (1.00s)"`).join('; ');

test('run log: >1 MB output with FAIL on the last line is captured completely with the real exit code', (t) => {
  const { d, bin } = fakeBinary(t, `i=0; while [ $i -lt 30000 ]; do echo "R7_SIM noise line $i ................................"; i=$((i+1)); done; ${PASSING}; echo "--- FAIL: TestR7Sim20DevicesSoak (3600.00s)" >&2; echo "FAIL"; exit 1`);
  const r = runToFile(bin, [], process.env, pj(d, 'run1'), { sha: 'a'.repeat(40) });
  assert.ok(r.logBytes > 1024 * 1024, `log ${r.logBytes} bytes`);
  assert.equal(r.exitCode, 1); assert.equal(r.signal, null); assert.equal(r.timedOut, false);
  const v = evaluateRun(r, REQUIRED);
  assert.match(v.tail, /FAIL$/); assert.match(v.tail, /--- FAIL: TestR7Sim20DevicesSoak/);
  assert.match(v.problems.join(';'), /exit code 1/); assert.match(v.problems.join(';'), /did not PASS/);
  const meta = JSON.parse(rf(pj(d, 'run1', 'meta.json'), 'utf8'));
  assert.equal(meta.sha, 'a'.repeat(40)); assert.ok(meta.startedAt <= meta.finishedAt); assert.equal(meta.exitCode, 1);
  assert.throws(() => runToFile(bin, [], process.env, pj(d, 'run1')), /fresh directory/, 'a run directory is never reused');
});

test('run log: timeout, signal, SKIP, missing or extra test, and exit 0 without PASS all fail the gate; a clean run passes', (t) => {
  const cases = [
    ['timeout', `echo started; sleep 30`, { timeoutMs: 800 }, /timed out/],
    ['signal', `${PASSING}; echo PASS; kill -TERM $$`, {}, /killed by SIGTERM/],
    ['skip', `${PASSING}; echo "--- SKIP: TestR7Sim20DevicesSoak (0.00s)"; echo PASS`, {}, /skipped/],
    ['missing', `echo "--- PASS: TestR7Sim1DeviceFullLoop (1.00s)"; echo PASS`, {}, /required test not PASSed/],
    ['extra', `${PASSING}; echo "--- PASS: TestSomethingElse (1.00s)"; echo PASS`, {}, /unexpected tests ran/],
    ['no-pass-line', `${PASSING}; exit 0`, {}, /did not PASS/],
  ];
  for (const [name, body, opts, want] of cases) {
    const { d, bin } = fakeBinary(t, body);
    const r = runToFile(bin, [], process.env, pj(d, 'run'), opts);
    const v = evaluateRun(r, REQUIRED);
    assert.match(v.problems.join(';'), want, name);
  }
  const { d, bin } = fakeBinary(t, `${PASSING}; echo PASS`);
  assert.deepEqual(evaluateRun(runToFile(bin, [], process.env, pj(d, 'ok')), REQUIRED).problems, []);
});

test('run log: the database password never reaches the log or meta.json', (t) => {
  const secret = 'f'.repeat(48);
  const { d, bin } = fakeBinary(t, `echo "connecting postgres://a1test:$PW@127.0.0.1/x"; echo "PGPASSWORD=$PW" >&2; ${PASSING}; echo PASS`);
  const r = runToFile(bin, ['--dsn', `postgres://a1test:${secret}@h/x`], { ...process.env, PW: secret }, pj(d, 'run'), { redact: [secret] });
  for (const f of ['test.log', 'meta.json']) assert.ok(!rf(pj(d, 'run', f), 'utf8').includes(secret), f);
  assert.match(rf(pj(d, 'run', 'test.log'), 'utf8'), /a1test:\*\*\*@/);
  assert.ok(ex(pj(d, 'run', 'meta.json')));
});

test('runner: binary output goes to a per-run file, never a pipe; cleanup in finally', () => {
  const src = rf('tools/r7-sim-e2e.mjs', 'utf8');
  assert.match(src, /stdio: \['ignore', fd, fd\]/);
  assert.match(src, /run-\$\{new Date\(\)\.toISOString\(\)/);
  assert.match(src, /redact: \[password\]/);
  assert.match(src, /\} finally \{\n\s+spawnSync\('docker', \['rm', '-f', pg\]/);
  assert.doesNotMatch(src.slice(src.indexOf('const runDir')), /run\(binary/);
});
