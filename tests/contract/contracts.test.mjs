import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import Ajv from 'ajv/dist/2020.js';
import formats from 'ajv-formats';
import YAML from 'yaml';
const read = async (p) => JSON.parse(await readFile(p, 'utf8'));
test('all generated envelopes validate; malformed topic identity rejected', async () => {
  const ajv = new Ajv({ strict: true });
  formats(ajv);
  for (const name of ['message', 'shadow', 'livesession'])
    ajv.addSchema(await read(`contracts/schemas/v1/${name}.json`));
  const sample = await read('tests/fixtures/birth.json');
  const validate = ajv.getSchema('https://contracts.invalid/crane/v1/message.json');
  assert.ok(validate(sample), JSON.stringify(validate.errors));
  assert.equal(validate({ ...sample, deviceId: '+/unauthorized' }), false);
  assert.equal(validate({ ...sample, sequence: -1 }), false);
  assert.equal(validate({ ...sample, kind: 'PLC_WRITE' }), false);
  assert.equal(validate({ ...sample, secret: 'unexpected' }), false);
});
test('REST paths and MQTT bindings remain frozen', async () => {
  const api = YAML.parse(await readFile('contracts/openapi/cloud-api.yaml', 'utf8'));
  assert.equal(api.openapi, '3.1.0');
  assert.equal(Object.keys(api.paths).length, 11);
  const async = YAML.parse(await readFile('contracts/asyncapi/edge-mqtt.yaml', 'utf8'));
  const model = await read('contracts/model.json');
  assert.equal(Object.keys(async.channels).length, 9);
  for (const topic of Object.keys(model.topics))
    assert.ok(Object.values(async.channels).some((c) => c.address.endsWith('/' + topic)));
  assert.equal(model.topics['alarm'].qos, 1);
  assert.equal(model.topics['telemetry/live'].qos, 0);
});
