// H2-1C: the operations summary contract is now GENERATED from contracts/model.json
// (tools/codegen/contracts.mjs → contracts/schemas/v1/*.json, Go/TS types; tools/codegen/openapi.mjs → cloud-api.yaml).
// This test validates the generated artefacts as written — nothing is patched before validation:
//   1. the generated cloud-api.yaml (with the new path) is a valid OpenAPI 3.1 document;
//   2. the path accepts only region / alarmWindow, uses the real webSession scheme, has 200/400/401/404/503;
//   3. every generated object schema reachable from OperationsSummary is closed and all properties are required;
//      only scope.region is nullable;
//   4. the generated schema is ISOMORPHIC to the H2-1B target contract (operations-summary.openapi.json):
//      same field paths, types, enums/consts, bounds and nullability after normalising named types;
//   5. JSON Schema (ajv 2020) on contracts/schemas/v1/operationssummary.json: legitimate zeros pass; null, missing,
//      string or negative counts, numeric operation, unknown fields, out-of-range freshness and >100 review items fail;
//   6. the generated Go types and TypeScript interfaces carry the same field set (and *int / number | null for region).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import SwaggerParser from '@apidevtools/swagger-parser';
import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';

const generated = readFileSync('contracts/openapi/cloud-api.yaml', 'utf8');
const spec = JSON.parse(generated);
const target = JSON.parse(readFileSync('docs/04-前端与可视化/evidence/homepage-h2-1b/operations-summary.openapi.json', 'utf8'));
const summarySchema = JSON.parse(readFileSync('contracts/schemas/v1/operationssummary.json', 'utf8'));

test('generated cloud-api.yaml is a valid OpenAPI 3.1 document as written', async () => {
  const api = await SwaggerParser.validate(JSON.parse(generated));
  assert.equal(api.openapi, '3.1.0');
});

test('operations summary path: parameters, security and responses', () => {
  const op = spec.paths['/api/v1/operations/summary']?.get;
  assert.ok(op, 'path missing');
  assert.equal(op.operationId, 'getOperationsSummary');
  assert.deepEqual(op.parameters.map((p) => p.name).sort(), ['alarmWindow', 'region']);
  assert.deepEqual(op.parameters.find((p) => p.name === 'alarmWindow').schema.enum, ['PT1H', 'PT6H', 'PT24H', 'P7D']);
  assert.equal(op.parameters.find((p) => p.name === 'region').schema.pattern, '^[0-9]{6}$');
  assert.deepEqual(op.security, [{ webSession: [] }]);
  assert.deepEqual(spec.components.securitySchemes.webSession, target.components.securitySchemes.webSession);
  for (const code of ['200', '400', '401', '404', '503']) assert.ok(op.responses[code]?.description, code);
  assert.equal(op.responses['200'].content['application/json'].schema.$ref, '#/components/schemas/OperationsSummary');
});

// Normalise a schema to a structural tree: named refs are followed, so generated named types
// (OperationsScope, ...) and the target's inline objects compare equal when they mean the same thing.
function normalise(schema, defs, seen = []) {
  if (schema.$ref) {
    const name = schema.$ref.split('/').at(-1);
    if (seen.includes(name)) return { cycle: name };
    return normalise(defs[name], defs, [...seen, name]);
  }
  if (schema.allOf?.length === 1) return normalise(schema.allOf[0], defs, seen);
  let nullable = false;
  if (schema.anyOf) {
    const nonNull = schema.anyOf.filter((s) => s.type !== 'null');
    nullable = nonNull.length < schema.anyOf.length;
    assert.equal(nonNull.length, 1);
    schema = nonNull[0];
  }
  let type = schema.type;
  if (Array.isArray(type)) {
    nullable = type.includes('null');
    type = type.filter((t) => t !== 'null')[0];
  }
  const out = { type, nullable };
  for (const k of ['minimum', 'maximum', 'pattern', 'format', 'maxItems']) if (schema[k] !== undefined) out[k] = schema[k];
  const enumValues = schema.enum ?? (schema.const !== undefined ? [schema.const] : undefined);
  if (enumValues) out.enum = enumValues;
  if (type === 'object') {
    out.closed = schema.additionalProperties === false;
    out.required = [...(schema.required ?? [])].sort();
    out.properties = Object.fromEntries(Object.entries(schema.properties).sort().map(([k, v]) => [k, normalise(v, defs, seen)]));
  }
  if (type === 'array') out.items = normalise(schema.items, defs, seen);
  return out;
}

test('generated OperationsSummary is isomorphic to the H2-1B target contract (plus scope.geoIndexVersion, assets.atScopeLevel)', () => {
  const gen = normalise({ $ref: '#/components/schemas/OperationsSummary' }, spec.components.schemas);
  const tgt = normalise({ $ref: '#/components/schemas/OperationsSummary' }, target.components.schemas);
  // H2-1C additions over the H2-1B target (documented in the contract): backend index version and
  // assets attributable only at the requested region level (no complete sub-region code).
  const additions = [['scope', 'geoIndexVersion'], ['metrics', 'assets', 'atScopeLevel']];
  const strip = (tree) => {
    const copy = structuredClone(tree);
    for (const path of additions) {
      let node = copy;
      for (const p of path.slice(0, -1)) node = node.properties[p];
      const last = path.at(-1);
      delete node.properties[last];
      node.required = node.required.filter((r) => r !== last);
    }
    return copy;
  };
  // Name/region-name length bounds are additions too; compare everything else.
  const dropLen = (t) => JSON.parse(JSON.stringify(t, (k, v) => (k === 'minLength' || k === 'maxLength' ? undefined : v)));
  assert.deepEqual(dropLen(strip(gen)), dropLen(tgt));
});

test('every generated object is closed, fully required, and only scope.region is nullable', () => {
  const nullable = [];
  (function walk(s, path, defs, seen) {
    if (s.$ref) {
      const name = s.$ref.split('/').at(-1);
      if (!seen.has(name)) { seen.add(name); walk(defs[name], `#/components/schemas/${name}`, defs, seen); }
      return;
    }
    if (s.anyOf) {
      if (s.anyOf.some((x) => x.type === 'null')) nullable.push(path);
      s.anyOf.forEach((x) => walk(x, path, defs, seen));
      return;
    }
    if (s.type === 'object') {
      assert.equal(s.additionalProperties, false, path);
      assert.deepEqual([...s.required].sort(), Object.keys(s.properties).sort(), path);
      for (const [k, v] of Object.entries(s.properties)) walk(v, `${path}/${k}`, defs, seen);
    }
    if (s.type === 'array') walk(s.items, `${path}[]`, defs, seen);
  })({ $ref: '#/components/schemas/OperationsSummary' }, '', spec.components.schemas, new Set());
  assert.deepEqual(nullable, ['#/components/schemas/OperationsScope/region']);
});

const zero = () => ({
  schemaVersion: 1,
  sampledAt: '2026-11-07T08:00:00Z',
  generatedAt: '2026-11-07T08:00:00.041Z',
  scope: { region: null, regionName: '全部授权范围', authorizedProjects: 0, geoIndexVersion: 'a'.repeat(64) },
  freshness: { staleAfterSeconds: 2100, maxFutureSkewSeconds: 120, alarmWindow: 'PT24H', windowStart: '2026-11-06T08:00:00Z', windowEnd: '2026-11-07T08:00:00Z' },
  metrics: {
    projects: 0,
    assets: { registered: 0, draft: 0, outOfService: 0, noRuntime: 0, unlocated: 0, atScopeLevel: 0 },
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
export function summaryValidator() {
  const ajv = new Ajv2020({ strict: false, allErrors: true });
  addFormats(ajv);
  return ajv.compile(summarySchema);
}

test('generated JSON Schema: legitimate zeros and populated payloads pass', () => {
  const v = summaryValidator();
  assert.ok(v(zero()), JSON.stringify(v.errors));
  const s = zero();
  s.scope = { region: 330000, regionName: '浙江省', authorizedProjects: 1, geoIndexVersion: 'b'.repeat(64) };
  s.metrics.real.pendingReview = 1;
  s.metrics.real.pendingReviewAssets = [{ id: 'A9', assetStatus: 'OUT_OF_SERVICE', alarm: true }];
  s.regions = [{ adcode: 330400, name: '嘉兴市', registered: 3, realConnected: 1, realAlarmDevices: 1 }];
  assert.ok(v(s), JSON.stringify(v.errors));
});

test('generated JSON Schema rejects null / missing / wrong-typed counts and fake capability numbers', () => {
  const v = summaryValidator();
  const bad = [
    ['null count', (s) => { s.metrics.real.connected = null; }],
    ['missing count', (s) => { delete s.metrics.alarm.devicesAll; }],
    ['missing nested cell', (s) => { delete s.metrics.alarm.records.cells.TEST.unattributed; }],
    ['string count', (s) => { s.metrics.assets.registered = '0'; }],
    ['negative count', (s) => { s.metrics.projects = -1; }],
    ['fractional count', (s) => { s.metrics.projects = 1.5; }],
    ['numeric operation', (s) => { s.metrics.operation = 0; }],
    ['null pendingEvents', (s) => { s.metrics.pendingEvents = null; }],
    ['unknown field', (s) => { s.metrics.realRunning = 3; }],
    ['skew above contract', (s) => { s.freshness.maxFutureSkewSeconds = 7200; }],
    ['stale below contract', (s) => { s.freshness.staleAfterSeconds = 299; }],
    ['null regionName', (s) => { s.scope.regionName = null; }],
    ['missing geoIndexVersion', (s) => { delete s.scope.geoIndexVersion; }],
    ['RETIRED in review list', (s) => { s.metrics.real.pendingReviewAssets = [{ id: 'A5', assetStatus: 'RETIRED', alarm: false }]; }],
    ['101 review items', (s) => { s.metrics.real.pendingReviewAssets = Array.from({ length: 101 }, (_, i) => ({ id: 'A' + i, assetStatus: 'DRAFT', alarm: false })); }],
    ['null regions', (s) => { s.regions = null; }],
  ];
  for (const [label, mutate] of bad) {
    const s = zero();
    mutate(s);
    assert.equal(v(s), false, label);
  }
});

test('generated Go struct and TypeScript interface carry the same fields', () => {
  const go = readFileSync('contracts/go/types.gen.go', 'utf8');
  const ts = readFileSync('contracts/typescript/types.gen.ts', 'utf8');
  for (const [name, schema] of Object.entries(spec.components.schemas).filter(([n]) => n.startsWith('Operations') || ['NotAvailable', 'RuntimeSourceCounts', 'AlarmAttributionCell'].includes(n))) {
    const goBody = go.match(new RegExp(`type ${name} struct \\{([^}]*)\\}`))?.[1];
    const tsBody = ts.match(new RegExp(`export interface ${name} \\{([^}]*)\\}`))?.[1];
    assert.ok(goBody && tsBody, name);
    const goFields = [...goBody.matchAll(/json:"([^"]+)"/g)].map((m) => m[1]).sort();
    const tsFields = [...tsBody.matchAll(/^\s+(\w+):/gm)].map((m) => m[1]).sort();
    assert.deepEqual(goFields, Object.keys(schema.properties).sort(), name + ' Go');
    assert.deepEqual(tsFields, Object.keys(schema.properties).sort(), name + ' TS');
  }
  assert.match(go, /Region\s+\*int\s+`json:"region"/);
  assert.match(ts, /region: number \| null;/);
});

// Real responses produced by the Go HTTP test against PostgreSQL (TestOperationsSummaryHTTP with
// OPS_SUMMARY_SAMPLES_DIR) are committed under docs/.../evidence/homepage-h2-1c/responses and validated
// here with the generated JSON Schema — not hand-written samples.
test('real API responses validate against the generated JSON Schema', async () => {
  const { readdirSync } = await import('node:fs');
  const dir = 'docs/04-前端与可视化/evidence/homepage-h2-1c/responses';
  const files = readdirSync(dir).filter((f) => f.endsWith('.json'));
  assert.ok(files.length >= 5, 'expected recorded real responses');
  const v = summaryValidator();
  for (const f of files) {
    const body = JSON.parse(readFileSync(`${dir}/${f}`, 'utf8'));
    assert.ok(v(body), `${f}: ${JSON.stringify(v.errors)}`);
  }
});
