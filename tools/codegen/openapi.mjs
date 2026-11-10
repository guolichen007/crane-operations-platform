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
// Portfolio (project intake / scheduling / resource / conflict) routes.
const portfolioScope = { $ref: '#/components/schemas/PortfolioScope' },
  projectIntake = { $ref: '#/components/schemas/ProjectIntake' },
  resource = { $ref: '#/components/schemas/Resource' },
  scheduleItem = { $ref: '#/components/schemas/ScheduleItem' },
  scheduleConflict = { $ref: '#/components/schemas/ScheduleConflict' },
  scheduleDependency = { $ref: '#/components/schemas/ScheduleDependency' },
  scheduleBaseline = { $ref: '#/components/schemas/ScheduleBaseline' };
add('/api/v1/portfolio/scopes', 'get', 'listPortfolioScopes', { type: 'array', items: portfolioScope });
add('/api/v1/portfolio/intakes', 'get', 'listIntakes', { type: 'array', items: projectIntake });
add('/api/v1/portfolio/intakes', 'post', 'createIntake', projectIntake, '201');
add('/api/v1/portfolio/intakes/{id}', 'get', 'getIntake', projectIntake);
add('/api/v1/portfolio/intakes/{id}/transitions', 'post', 'transitionIntake', null);
add('/api/v1/portfolio/intakes/{id}/approve', 'post', 'approveIntake', null);
add('/api/v1/portfolio/resources', 'get', 'listResources', { type: 'array', items: resource });
add('/api/v1/portfolio/resources', 'post', 'createResource', resource, '201');
add('/api/v1/portfolio/resources/{id}', 'get', 'getResource', resource);
add('/api/v1/portfolio/resources/{id}/calendar', 'get', 'getResourceCalendar', null);
add('/api/v1/portfolio/resources/{id}/calendar', 'put', 'putResourceCalendar', null);
add('/api/v1/portfolio/schedule/dependencies', 'get', 'listScheduleDependencies', { type: 'array', items: scheduleDependency });
add('/api/v1/portfolio/schedule/dependencies', 'post', 'createScheduleDependency', scheduleDependency, '201');
add('/api/v1/portfolio/schedule/items', 'get', 'listScheduleItems', { type: 'array', items: scheduleItem });
add('/api/v1/portfolio/schedule/items', 'post', 'createScheduleItem', scheduleItem, '201');
add('/api/v1/portfolio/schedule/items/{id}', 'get', 'getScheduleItem', scheduleItem);
add('/api/v1/portfolio/schedule/items/{id}/reservations', 'post', 'createReservation', { $ref: '#/components/schemas/ResourceReservation' }, '201');
add('/api/v1/portfolio/schedule/approve', 'post', 'approveSchedule', null);
add('/api/v1/portfolio/conflicts', 'get', 'listConflicts', { type: 'array', items: scheduleConflict });
add('/api/v1/portfolio/conflicts/{id}', 'get', 'getConflict', scheduleConflict);
add('/api/v1/portfolio/conflicts/{id}/override', 'post', 'overrideConflict', null);
add('/api/v1/portfolio/baselines', 'get', 'listBaselines', { type: 'array', items: scheduleBaseline });
add('/api/v1/portfolio/baselines', 'post', 'createBaseline', scheduleBaseline, '201');
// Q2 formal projects (approved intakes) and their initial project grants (portfolio-scoped).
const portfolioProject = { $ref: '#/components/schemas/PortfolioProject' },
  projectMember = { $ref: '#/components/schemas/ProjectMember' };
add('/api/v1/portfolio/projects', 'get', 'listPortfolioProjects', { type: 'array', items: portfolioProject });
add('/api/v1/portfolio/projects/{id}', 'get', 'getPortfolioProject', portfolioProject);
add('/api/v1/portfolio/projects/{id}/members', 'get', 'listPortfolioProjectMembers', { type: 'array', items: projectMember });
add('/api/v1/portfolio/projects/{id}/members', 'post', 'changePortfolioProjectMember', { type: 'array', items: projectMember });
// Asset master-data business routes.
const assetMaster = { $ref: '#/components/schemas/AssetMasterData' },
  assetLocation = { $ref: '#/components/schemas/AssetLocation' },
  manufacturer = { $ref: '#/components/schemas/Manufacturer' },
  catalogItem = { $ref: '#/components/schemas/EquipmentCatalogItem' },
  equipmentInstallation = { $ref: '#/components/schemas/EquipmentInstallation' },
  equipmentRelation = { $ref: '#/components/schemas/EquipmentRelation' };
add('/api/v1/assets', 'get', 'listAssetMasterData', { type: 'array', items: assetMaster });
add('/api/v1/assets', 'post', 'createAssetMasterData', assetMaster, '201');
add('/api/v1/assets/{id}', 'get', 'getAssetMasterData', assetMaster);
add('/api/v1/assets/{id}', 'patch', 'updateAssetMasterData', assetMaster);
add('/api/v1/assets/{id}/retire', 'post', 'retireAsset', null);
add('/api/v1/assets/{id}/move', 'post', 'moveAsset', null);
add('/api/v1/projects/{projectId}/locations', 'get', 'listLocations', { type: 'array', items: assetLocation });
add('/api/v1/projects/{projectId}/locations', 'post', 'createLocation', assetLocation, '201');
add('/api/v1/locations/{id}', 'get', 'getLocation', assetLocation);
add('/api/v1/locations/{id}', 'patch', 'updateLocation', assetLocation);
add('/api/v1/locations/{id}/disable', 'post', 'disableLocation', null);
add('/api/v1/manufacturers', 'get', 'listManufacturers', { type: 'array', items: manufacturer });
add('/api/v1/manufacturers', 'post', 'createManufacturer', manufacturer, '201');
add('/api/v1/manufacturers/{id}', 'get', 'getManufacturer', manufacturer);
add('/api/v1/manufacturers/{id}', 'patch', 'updateManufacturer', manufacturer);
add('/api/v1/manufacturers/{id}/disable', 'post', 'disableManufacturer', null);
add('/api/v1/equipment/types', 'get', 'listEquipmentTypes', { type: 'array', items: { $ref: '#/components/schemas/EquipmentType' } });
add('/api/v1/equipment/catalog', 'get', 'listCatalogItems', { type: 'array', items: catalogItem });
add('/api/v1/equipment/catalog', 'post', 'createCatalogItem', catalogItem, '201');
add('/api/v1/equipment/catalog/{id}', 'get', 'getCatalogItem', catalogItem);
add('/api/v1/equipment/catalog/{id}', 'patch', 'updateCatalogItem', catalogItem);
add('/api/v1/equipment/catalog/{id}/disable', 'post', 'disableCatalogItem', null);
add('/api/v1/assets/{assetId}/equipment', 'get', 'listEquipment', { type: 'array', items: equipmentInstallation });
add('/api/v1/assets/{assetId}/equipment', 'post', 'createEquipment', equipmentInstallation, '201');
add('/api/v1/equipment/{id}', 'get', 'getEquipment', equipmentInstallation);
add('/api/v1/equipment/{id}', 'patch', 'updateEquipment', equipmentInstallation);
add('/api/v1/equipment/{id}/remove', 'post', 'removeEquipment', null);
add('/api/v1/assets/{assetId}/equipment-relations', 'get', 'listEquipmentRelations', { type: 'array', items: equipmentRelation });
add('/api/v1/assets/{assetId}/equipment-relations', 'post', 'createEquipmentRelation', equipmentRelation, '201');
add('/api/v1/equipment-relations/{id}/remove', 'post', 'removeEquipmentRelation', null);
// Operations summary (Homepage H2-1C): read-only, authorized-scope statistics. Only region / alarmWindow
// are accepted; 400 malformed parameter, 404 unknown region, 503 any required step failed (never partial).
add('/api/v1/operations/summary', 'get', 'getOperationsSummary', { $ref: '#/components/schemas/OperationsSummary' });
Object.assign(paths['/api/v1/operations/summary'].get, {
  security: [{ webSession: [] }],
  parameters: [
    { name: 'region', in: 'query', required: false, description: '6-digit administrative code; omitted = whole authorized scope', schema: { type: 'string', pattern: '^[0-9]{6}$' } },
    { name: 'alarmWindow', in: 'query', required: false, schema: { type: 'string', enum: ['PT1H', 'PT6H', 'PT24H', 'P7D'], default: 'PT24H' } },
  ],
});
Object.assign(paths['/api/v1/operations/summary'].get.responses, {
  200: { ...paths['/api/v1/operations/summary'].get.responses['200'], description: 'Complete summary from one database snapshot', headers: { 'Cache-Control': { schema: { type: 'string', const: 'no-store' } } } },
  400: { description: 'Malformed region or alarmWindow' },
  404: { description: 'Region code not in the administrative index' },
  503: { description: 'Authorization, statistics query or administrative index unavailable; no partial statistics' },
});
delete paths['/api/v1/operations/summary'].get.responses['403'];
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
