import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import Ajv from 'ajv/dist/2020.js';
import formats from 'ajv-formats';
import YAML from 'yaml';
import SwaggerParser from '@apidevtools/swagger-parser';
import { Parser } from '@asyncapi/parser';
import { resolve } from 'node:path';
const read = async (p) => JSON.parse(await readFile(p, 'utf8'));
test('REST and MQTT documents pass standards parsers; legacy security shape is rejected', async () => {
  await SwaggerParser.validate(resolve('contracts/openapi/cloud-api.yaml'));
  const spec = await read('contracts/asyncapi/edge-mqtt.yaml');
  const parser = new Parser();
  const result = await parser.parse(spec);
  assert.deepEqual(
    result.diagnostics.filter((d) => d.severity === 0),
    []
  );
  assert.ok(result.document);
  const invalid = structuredClone(spec);
  invalid.servers.dev.security = [{ deviceCertificate: [] }];
  const rejected = await parser.parse(invalid);
  assert.ok(rejected.diagnostics.some((d) => d.severity === 0));
});
test('all generated envelopes validate; malformed topic identity rejected', async () => {
  const ajv = new Ajv({ strict: true });
  formats(ajv);
  for (const name of await readdir('contracts/schemas/v1'))
    ajv.addSchema(await read(`contracts/schemas/v1/${name}`));
  const sample = await read('tests/fixtures/birth.json');
  const validate = ajv.getSchema('https://contracts.invalid/crane/v1/envelope.json');
  assert.ok(validate(sample), JSON.stringify(validate.errors));
  assert.equal(validate({ ...sample, deviceId: '+/unauthorized' }), false);
  assert.equal(validate({ ...sample, sequence: -1 }), false);
  assert.equal(validate({ ...sample, kind: 'PLC_WRITE' }), false);
  assert.equal(validate({ ...sample, secret: 'unexpected' }), false);
  assert.equal(validate({ ...sample, bootSeq: 0 }), false);
  assert.equal(validate({ ...sample, payload: { ...sample.payload, x: 1 } }), false);
  const watch = { ...sample, kind: 'WATCH_STOP', payload: { leaseId: 'lease-1', generation: 2, ttlSeconds: 5, telemetryHz: 1, expiresAt: sample.sentAt } };
  assert.ok(validate(watch), JSON.stringify(validate.errors));
  assert.equal(validate({ ...watch, kind: 'LIVE' }), false);
  assert.equal(validate({ ...watch, payload: { ...watch.payload, load: 1 } }), false);
  const async = await read('contracts/asyncapi/edge-mqtt.yaml');
  for (const [kind, message] of Object.entries(async.components.messages)) {
    assert.equal(message.payload.schema.properties.kind.const, kind);
    assert.equal(message.payload.schema.properties.payload.type, 'object');
    assert.equal(message.payload.schema.properties.payload.additionalProperties, false);
  }
});
test('REST inventory extension and frozen MQTT bindings remain compatible', async () => {
  const api = YAML.parse(await readFile('contracts/openapi/cloud-api.yaml', 'utf8'));
  assert.equal(api.openapi, '3.1.0');
  assert.equal(Object.keys(api.paths).length, 13);
  const async = YAML.parse(await readFile('contracts/asyncapi/edge-mqtt.yaml', 'utf8'));
  const model = await read('contracts/model.json');
  assert.equal(Object.keys(async.channels).length, 10);
  for (const topic of Object.keys(model.topics))
    assert.ok(Object.values(async.channels).some((c) => c.address.endsWith('/' + topic)));
  assert.equal(model.topics['alarm'].qos, 1);
  assert.equal(model.topics['telemetry/live'].qos, 0);
  for (const [topic, meta] of Object.entries(model.topics)) {
    const channel = Object.values(async.channels).find((c) => c.address.endsWith('/' + topic));
    assert.deepEqual(Object.keys(channel.messages), meta.kinds);
  }
});
