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
  assert.match(src, /gateProblems\(output, REQUIRED, \{ exact: true \}\)/);
  assert.match(src, /images\.mosquitto/);
  assert.match(src, /images\.postgres/);
});

test('harness drives the real chain: no route interception or canned responses', () => {
  const files = ['services/cloud-api/internal/app/r7_sim_harness_test.go', 'services/cloud-api/internal/app/r7_sim_e2e_test.go'].map((f) => readFileSync(f, 'utf8')).join('\n');
  for (const needle of ['runner.RunDevice(', 'startRuntime(', 'h.register(mux)', 'websocket.Dial(', 'fleet.ACL(', 'runtimeidentity.Apply(']) assert.ok(files.includes(needle), needle);
  assert.doesNotMatch(files, /page\.route|httpmock|fulfill\(/);
  const tests = [...files.matchAll(/^func (TestR7Sim\w+)\(/gm)].map((m) => m[1]).sort();
  assert.deepEqual(tests, REQUIRED.filter((t) => t.startsWith('TestR7Sim')).sort());
  assert.match(readFileSync('services/cloud-api/internal/app/r7_restore_drill_test.go', 'utf8'), /crane_drill_/);
});
