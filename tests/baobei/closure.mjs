import assert from 'node:assert/strict';
import {readFileSync,readdirSync} from 'node:fs';
import {createRequire} from 'node:module';
const require=createRequire(new URL('../../apps/project-intake-web/package.json',import.meta.url));
const {parse}=require('yaml');
const read=(p)=>readFileSync(p,'utf8');
const PG='postgres:18.4-alpine@sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15';
const input={
  ingress:readdirSync('deploy/baobei/ingress',{recursive:true}).filter(f=>/example|template/.test(f)).map(f=>read('deploy/baobei/ingress/'+f)).join('\n'),
  nginx:read('deploy/baobei/nginx.production.conf'),
  compose:read('deploy/baobei/compose.production.yaml'),
  auth:read('services/project-intake-api/auth.go'),
  main:read('services/project-intake-api/main.go'),
  cert:read('deploy/baobei/cert/cert-renew.sh'),
  state:read('deploy/baobei/cert/cert-state.py'),
  service:read('deploy/baobei/cert/u-dao-baobei-cert-renew.service'),
  timer:read('deploy/baobei/cert/u-dao-baobei-cert-renew.timer'),
  provision:read('deploy/baobei/cert/provision-acme.sh'),
  keycloak:read('docs/项目报备中心/Keycloak生产配置契约.md'),
  publish:read('.github/workflows/baobei-publish.yml'),
};
export function validateClosure(x){
  assert.match(x.nginx,/listen\s+8080\s*;/);
  assert(!/listen\s+80\s*;/.test(x.nginx));
  assert.match(x.ingress,/resolver\s+127\.0\.0\.11\b/);
  assert.match(x.ingress,/http:\/\/baobei-web:8080\s*;/);
  // The boundary is essential: :8080 is not :80.
  assert(!/baobei-web:80(?!\d)/.test(x.ingress));
  for(const [,port] of x.ingress.matchAll(/baobei-web:(\d+)/g))assert.equal(port,'8080');
  assert(!/https?:\/\/(?:\d{1,3}\.){3}\d{1,3}/.test(x.ingress),'container IP forbidden');
  const c=parse(x.compose);assert.equal(c.services['baobei-postgres'].image,PG);
  for(const kind of ['WEB','API'])assert.match(c.services['baobei-'+kind.toLowerCase()].image,new RegExp('^\\$\\{BAOBEI_'+kind+'_IMAGE:\\?'));
  assert.match(x.auth,/roles := claims\.Resource\[a\.cfg\.ClientID\]\.Roles/);
  assert(!/roles := claims\.Realm/.test(x.auth));
  const callback=x.auth.slice(x.auth.indexOf('case "/auth/callback":'),x.auth.indexOf('case "/auth/logout":'));
  assert(!/INSERT\s+INTO\s+app_users/i.test(callback),'no first-login provision');
  assert.match(callback,/WHERE subject=\$1 AND active/);
  assert.match(x.main,/os\.Args\[2\] == "identity-apply"/);
  assert.match(read('services/project-intake-api/identity.go'),/func identityCommand\(/);
  for(const value of ['Full Scope Allowed | OFF','resource_access.${client_id}.roles','Add to ID token | true','Add to access token | true','role scope mappings'])assert(x.keycloak.includes(value),value);
  for(const value of ['DOMAIN=baobei.youdaozd.com','/srv/u-dao/secrets/baobei/acme','/srv/u-dao/platform/ingress/acme-webroot','/srv/u-dao/platform/ingress/tls/baobei','https://$DOMAIN/health/ready','recover_pending','nginx -t','nginx -s reload'])assert(x.cert.includes(value),value);
  for(const src of [x.cert,x.state,x.provision,x.service,x.timer])assert(!/crane\.youdaozd|firebot|secrets\/crane|tls\/crane|u-dao-crane-cert/.test(src),'ACME cross-product reference');
  assert(x.service.includes('ExecStart=/usr/local/sbin/u-dao-baobei-cert-renew --run'));
  assert(x.service.includes('ProtectSystem=strict')&&x.service.includes('PrivateTmp=true')&&x.service.includes('/srv/u-dao/secrets/baobei/acme'));
  assert(x.timer.includes('Unit=u-dao-baobei-cert-renew.service')&&x.timer.includes('Persistent=true'));
  for(const value of ['VERSION=3.1.6','verify-tag','807da6498377ee5e0cf43a78091f46f12dc59a89','c7d68b021cfd6380ea83a82962abde5b484779fee0b97d38681dfa1396bbc8d7'])assert(x.provision.includes(value));
  const workflow=parse(x.publish);assert.deepEqual(Object.keys(workflow.on),['workflow_dispatch']);
  assert.equal(workflow.on.workflow_dispatch.inputs.source_sha.required,true);
  assert.deepEqual(workflow.permissions,{contents:'read',packages:'write'});
  for(const f of ['.github/workflows/baobei-ci.yml','.github/workflows/baobei-publish.yml'])
    for(const [,ref] of read(f).matchAll(/uses:\s*([^\s#]+)/g))assert.match(ref,/@[a-f0-9]{40}$/);
  assert.equal(Object.keys(parse(read('.github/workflows/baobei-ci.yml')).jobs).length,8);
  const fixture=JSON.parse(read('services/project-intake-api/testdata/keycloak-id-token.synthetic.json'));
  assert.equal(fixture.typ,'ID');assert.equal(fixture.aud,'baobei-web');assert.deepEqual(fixture.resource_access['baobei-web'].roles,['baobei_sales']);
}
validateClosure(input);
for(const [name,mutate]of [
 ['port80',x=>x.nginx=x.nginx.replace('listen 8080;','listen 80;')],
 ['upstream80',x=>x.ingress=x.ingress.replace('baobei-web:8080','baobei-web:80')],
 ['upstream8081',x=>x.ingress=x.ingress.replace('baobei-web:8080','baobei-web:8081')],
 ['container-IP',x=>x.ingress+='\nproxy_pass http://172.19.0.7:8080;'],
 ['tag-only-PG',x=>x.compose=x.compose.replace(PG,'postgres:18.4-alpine')],
 ['realm-role',x=>x.auth=x.auth.replace('roles := claims.Resource[a.cfg.ClientID].Roles','roles := claims.Realm.Roles')],
 ['auto-provision',x=>x.auth=x.auth.replace('case "/auth/callback":','case "/auth/callback":\n INSERT INTO app_users')],
 ['missing-CLI',x=>x.main=x.main.replace('"identity-apply"','"missing"')],
 ['ACME-cross-product',x=>x.cert=x.cert.replace('secrets/baobei/acme','secrets/crane-ops/acme')],
 ['ACME-unit-cross-product',x=>x.service=x.service.replace('u-dao-baobei-cert-renew','u-dao-crane-cert-renew')],
 ['full-scope-ON',x=>x.keycloak=x.keycloak.replace('Full Scope Allowed | OFF','Full Scope Allowed | ON')],
 ['push-publish',x=>x.publish=x.publish.replace('  workflow_dispatch:','  push: {}\n  workflow_dispatch:')],
]){const x=structuredClone(input);mutate(x);assert.throws(()=>validateClosure(x),name);}
console.log('WEB_CONTAINER_PORT=8080 SHARED_INGRESS_UPSTREAM=baobei-web:8080');
console.log('WEB_IMAGE_REQUIRES_REGISTRY_DIGEST=YES API_IMAGE_REQUIRES_REGISTRY_DIGEST=YES POSTGRES_PINNED_DIGEST=YES PRODUCTION_POSTGRES_PINNED=YES');
console.log('REALM_ROLE_AUTHORIZED=NO CLIENT_ROLE_ID_TOKEN_REQUIRED=YES PRODUCTION_IDENTITY_AUTO_PROVISION=NO IDENTITY_APPLY_AVAILABLE=YES BAOBEI_ACME_ISOLATED_FROM_CRANE=YES');
console.log('Production closure and 12 negative contracts PASS');
