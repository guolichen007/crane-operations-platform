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
  message = { $ref: '#/components/schemas/Envelope' };
add('/api/v1/projects', 'get', 'listProjects', { type: 'array', items: { $ref: '#/components/schemas/InventoryProject' } });
add('/api/v1/assets/devices', 'get', 'listAssets', { type: 'array', items: { $ref: '#/components/schemas/AssetDevice' } });
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
add('/auth/login', 'get', 'beginOIDCLogin', null, '302');
add('/auth/callback', 'get', 'finishOIDCLogin', null, '303');
add('/auth/session', 'get', 'getWebSession', { $ref: '#/components/schemas/WebSession' });
add('/auth/logout', 'post', 'logoutWebSession', null, '204');
paths['/auth/logout'].post.responses['204'].headers = { Location: { description: 'Optional OIDC RP-initiated logout URL with client_id and exact post_logout_redirect_uri; contains no token', schema: { type: 'string', format: 'uri' } } };
for (const path of ['/auth/login', '/auth/callback']) {
  paths[path].get.security = [];
  paths[path].get['x-security-transaction-only'] = true;
}
paths['/api/v1/auth/dev-session'].post['x-development-only'] = true;
for (const [path, methods] of Object.entries(paths)) for (const [method, operation] of Object.entries(methods)) {
  if (['post','put','patch','delete'].includes(method) && path !== '/api/v1/auth/dev-session') {
    operation.parameters.push({ name: 'X-CSRF-Token', in: 'header', required: true, schema: { type: 'string', minLength: 32 } });
    if (path.startsWith('/api/')) operation['x-project-scope-first'] = true;
  }
}
const schemas = {};
const model = JSON.parse(await readFile('contracts/model.json', 'utf8'));
for (const name of [...Object.keys(model.types), 'Envelope']) {
  const schema = JSON.parse(
    await readFile(`contracts/schemas/v1/${name.toLowerCase()}.json`, 'utf8')
  );
  delete schema.$id;
  delete schema.$schema;
  delete schema.$defs;
  schemas[name] = JSON.parse(JSON.stringify(schema).replaceAll('#/$defs/', '#/components/schemas/'));
}
const spec = {
  openapi: '3.1.0',
  info: {
    title: 'Crane Cloud API',
    version: '1.0.0-rc.a1',
    description:
      'A1 same-origin BFF: OIDC Authorization Code + PKCE, HttpOnly opaque server Session and session-bound CSRF. Project scope precedes role checks. A 201 creates only a Viewer Lease; LIVE requires Edge ACK. Remote control and pointcloud remain disabled.',
  },
  servers: [{ url: '/' }],
  security: [{ webSession: [] }, { devBearer: [] }, { devCookie: [] }],
  paths,
  components: {
    securitySchemes: {
      webSession: { type: 'apiKey', in: 'cookie', name: '__Host-crane_session', description: 'Production: Secure, HttpOnly, SameSite=Lax, Path=/; hash-only PostgreSQL session. Development credentials are rejected in production.' },
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
