import { readFile, writeFile } from 'node:fs/promises';
const paths = {};
function add(path, method, operationId, response, status = '200') {
  const params = [...path.matchAll(/\{([^}]+)\}/g)].map((m) => ({
    name: m[1],
    in: 'path',
    required: true,
    schema: { type: 'string', pattern: '^[A-Za-z0-9_-]{1,80}$' },
  }));
  paths[path] ??= {};
  paths[path][method] = {
    operationId,
    parameters: params,
    responses: {
      [status]: {
        description: status === '501' ? 'Phase 2 disabled' : 'Success',
        ...(response ? { content: { 'application/json': { schema: response } } } : {}),
      },
      401: { description: 'Unauthorized' },
      403: { description: 'Forbidden' },
      404: { description: 'Not found' },
      503: { description: 'Dependency unavailable' },
    },
    ...(method !== 'get' ? { 'x-audit': true } : {}),
  };
}
const shadow = { $ref: '#/components/schemas/Shadow' },
  live = { $ref: '#/components/schemas/LiveSession' },
  message = { $ref: '#/components/schemas/Message' };
add('/api/v1/devices', 'get', 'listDevices', { type: 'array', items: shadow });
add('/api/v1/devices/{id}', 'get', 'getDevice', shadow);
add('/api/v1/devices/{id}/shadow', 'get', 'getShadow', shadow);
add('/api/v1/devices/{id}/live-sessions', 'post', 'openLiveSession', live, '201');
add('/api/v1/live-sessions/{sessionId}/renew', 'post', 'renewLiveSession', live);
add('/api/v1/live-sessions/{sessionId}', 'delete', 'closeLiveSession', null, '204');
add('/api/v1/devices/{id}/remote-sessions', 'post', 'openRemoteSession', null, '501');
add('/api/v1/remote-sessions/{sessionId}', 'delete', 'closeRemoteSession', null, '501');
add('/api/v1/alarms', 'get', 'listAlarms', { type: 'array', items: message });
add('/api/v1/auth/dev-session', 'post', 'developmentSession', {
  type: 'object',
  properties: { mode: { const: 'development-only' } },
});
add('/api/v1/devices/{id}/stream', 'get', 'streamShadow', null, '101');
const schemas = {};
for (const name of ['Message', 'Shadow', 'LiveSession']) {
  const schema = JSON.parse(
    await readFile(`contracts/schemas/v1/${name.toLowerCase()}.json`, 'utf8')
  );
  delete schema.$id;
  if (name === 'Shadow') schema.properties.reported = { $ref: '#/components/schemas/Message' };
  schemas[name] = schema;
}
const spec = {
  openapi: '3.1.0',
  info: {
    title: 'Crane Cloud API',
    version: '1.0.0',
    description:
      'Phase 0 development adapter. Production identity and remote/pointcloud are disabled.',
  },
  servers: [{ url: '/' }],
  security: [{ devBearer: [] }, { devCookie: [] }],
  paths,
  components: {
    securitySchemes: {
      devBearer: {
        type: 'http',
        scheme: 'bearer',
        description: 'Local random token; development only',
      },
      devCookie: { type: 'apiKey', in: 'cookie', name: 'crane_dev' },
    },
    schemas,
  },
};
const text = JSON.stringify(spec, null, 2) + '\n';
if (process.argv.includes('--check')) {
  const { readFile } = await import('node:fs/promises');
  if ((await readFile('contracts/openapi/cloud-api.yaml', 'utf8')) !== text)
    throw Error('OpenAPI drift');
} else await writeFile('contracts/openapi/cloud-api.yaml', text);
