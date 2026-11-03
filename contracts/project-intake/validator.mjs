import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
const require = createRequire(
  new URL('../../apps/project-intake-web/package.json', import.meta.url)
);
const Ajv = require('ajv/dist/2020');
const addFormats = require('ajv-formats');
const schema = JSON.parse(
  readFileSync(new URL('./ProjectIntakeTransferV1.schema.json', import.meta.url))
);
const ajv = new Ajv({ allErrors: true });
addFormats(ajv);
const validate = ajv.compile(schema);
const canonical = (value) =>
  Array.isArray(value)
    ? value.map(canonical)
    : value && typeof value === 'object'
      ? Object.fromEntries(
          Object.keys(value)
            .sort()
            .map((k) => [k, canonical(value[k])])
        )
      : value;
export function payloadHash(value) {
  const { payloadHash: ignored, ...payload } = value;
  void ignored;
  return createHash('sha256')
    .update(JSON.stringify(canonical(payload)))
    .digest('hex');
}
export function validateTransfer(value) {
  if (!validate(value)) throw Error(ajv.errorsText(validate.errors));
  if (payloadHash(value) !== value.payloadHash) throw Error('payloadHash mismatch');
  return {
    sourceIntakeId: value.sourceIntakeId,
    sourceRevision: value.sourceRevision,
    plannedEquipmentCount: value.plannedEquipment.length,
    connected: false,
  };
}
