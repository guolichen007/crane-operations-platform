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
