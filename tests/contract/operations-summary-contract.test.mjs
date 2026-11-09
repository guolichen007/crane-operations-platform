// H2-1B-4: validate the operations-summary OpenAPI 3.1 contract AS WRITTEN (no field is added or patched here).
// Run from the repository root:  node tests/contract/operations-summary-contract.test.mjs   (node --test)
// Checks:
//   1. swagger-parser validates the file as a complete OpenAPI 3.1 document;
//   2. the 200 response has its own description; the webSession scheme is byte-identical to the
//      generated contracts/openapi/cloud-api.yaml (reuse of the real scheme, not a stand-in);
//   3. every object property is required, additionalProperties is false, only scope.region is nullable;
//   4. JSON Schema validation (ajv 2020) of response payloads: a complete payload passes; null, missing,
//      string, negative counts and a numeric "operation" fail — absence is never accepted as 0.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import SwaggerParser from '@apidevtools/swagger-parser';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

const FILE = 'docs/04-前端与可视化/evidence/homepage-h2-1b/operations-summary.openapi.json';
const raw = readFileSync(FILE, 'utf8');
const spec = JSON.parse(raw);

test('valid OpenAPI 3.1 document as written', async () => {
  const api = await SwaggerParser.validate(JSON.parse(raw)); // fresh copy; validate() dereferences in place
  assert.equal(api.openapi, '3.1.0');
  assert.equal(JSON.stringify(spec), JSON.stringify(JSON.parse(raw)), 'validation must not rely on mutation');
});

test('200 response is fully specified and webSession is the real generated scheme', () => {
  const op = spec.paths['/api/v1/operations/summary'].get;
  assert.ok(op.responses['200'].description.length > 0);
  for (const code of ['400', '401', '404', '503']) assert.ok(op.responses[code]?.description, code);
  assert.deepEqual(op.security, [{ webSession: [] }]);
  const generated = JSON.parse(readFileSync('contracts/openapi/cloud-api.yaml', 'utf8'));
  assert.deepEqual(spec.components.securitySchemes.webSession, generated.components.securitySchemes.webSession);
  const names = op.parameters.map((p) => p.name).sort();
  assert.deepEqual(names, ['alarmWindow', 'region'], 'no project / status / source parameters');
});

test('every object property is required, closed, and only scope.region is nullable', () => {
  const nullable = [];
  (function walk(s, path) {
    if (!s || typeof s !== 'object') return;
    if (s.type === 'object' && s.properties) {
      assert.equal(s.additionalProperties, false, `${path} must be closed`);
      assert.deepEqual([...(s.required ?? [])].sort(), Object.keys(s.properties).sort(), `${path} required`);
    }
    if (Array.isArray(s.type) && s.type.includes('null')) nullable.push(path);
    if ('nullable' in s) assert.fail(`${path} uses OpenAPI 3.0 nullable`);
    for (const [k, v] of Object.entries(s)) walk(v, `${path}/${k}`);
  })(spec.components.schemas, '#/components/schemas');
  assert.deepEqual(nullable, ['#/components/schemas/OperationsSummary/properties/scope/properties/region']);
});

const sample = () => ({
  schemaVersion: 1,
  sampledAt: '2026-11-07T08:00:00Z',
  generatedAt: '2026-11-07T08:00:00.041Z',
  scope: { region: null, regionName: '全部授权范围', authorizedProjects: 0 },
  freshness: { staleAfterSeconds: 2100, maxFutureSkewSeconds: 120, alarmWindow: 'PT24H', windowStart: '2026-11-06T08:00:00Z', windowEnd: '2026-11-07T08:00:00Z' },
  metrics: {
    projects: 0,
    assets: { registered: 0, draft: 0, outOfService: 0, noRuntime: 0, unlocated: 0 },
    bound: { REAL: 0, SIMULATION: 0, TEST: 0 },
    real: { connected: 0, pendingReview: 0, pendingReviewAlarm: 0, pendingReviewAssets: [], stale: 0, unknownTime: 0, offline: 0, live: 0 },
    operation: { status: 'NOT_AVAILABLE' },
    alarm: {
      devices: { REAL: 0, SIMULATION: 0, TEST: 0 },
      devicesAll: 0,
      records: { cells: { REAL: { attributed: 0, unattributed: 0 }, SIMULATION: { attributed: 0, unattributed: 0 }, TEST: { attributed: 0, unattributed: 0 } }, total: 0, assets: 0, futureSkew: 0 },
    },
    pendingEvents: { status: 'NOT_AVAILABLE' },
  },
  regions: [],
});

function validator() {
  const ajv = new Ajv2020({ strict: false, allErrors: true });
  addFormats(ajv);
  ajv.addSchema({ $id: 'summary.json', components: spec.components });
  return ajv.compile({ $ref: 'summary.json#/components/schemas/OperationsSummary' });
}

test('legitimate zeros (authorized-but-empty scope) and a populated payload are valid', () => {
  const v = validator();
  assert.ok(v(sample()), JSON.stringify(v.errors));
  const s = sample();
  s.scope = { region: 330000, regionName: '浙江省', authorizedProjects: 1 };
  s.metrics.real.pendingReview = 1;
  s.metrics.real.pendingReviewAssets = [{ id: 'A9', assetStatus: 'OUT_OF_SERVICE', alarm: true }];
  s.regions = [{ adcode: 330400, name: '嘉兴市', registered: 3, realConnected: 1, realAlarmDevices: 1 }];
  assert.ok(v(s), JSON.stringify(v.errors));
});

test('null / missing / string / negative counts and fake capability numbers are rejected (never defaulted to 0)', () => {
  const v = validator();
  const bad = [
    ['null count', (s) => { s.metrics.real.connected = null; }],
    ['missing count', (s) => { delete s.metrics.alarm.devicesAll; }],
    ['missing nested cell', (s) => { delete s.metrics.alarm.records.cells.TEST.unattributed; }],
    ['string count', (s) => { s.metrics.assets.registered = '0'; }],
    ['negative count', (s) => { s.metrics.projects = -1; }],
    ['numeric operation instead of NOT_AVAILABLE', (s) => { s.metrics.operation = 0; }],
    ['null pendingEvents', (s) => { s.metrics.pendingEvents = null; }],
    ['unknown extra field', (s) => { s.metrics.realRunning = 3; }],
    ['skew above contract', (s) => { s.freshness.maxFutureSkewSeconds = 7200; }],
    ['negative skew', (s) => { s.freshness.maxFutureSkewSeconds = -1; }],
    ['null region name', (s) => { s.scope.regionName = null; }],
    ['RETIRED in review list', (s) => { s.metrics.real.pendingReviewAssets = [{ id: 'A5', assetStatus: 'RETIRED', alarm: false }]; }],
  ];
  for (const [label, mutate] of bad) {
    const s = sample();
    mutate(s);
    assert.equal(v(s), false, label);
  }
});
