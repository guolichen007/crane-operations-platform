"""Fixture for TestR1ERealSwitchDrill (loopback Keycloak only): prints shell exports.

Run after keycloak-realm-probe.py. Sets both baobei-web clients (crane and baobei
realms) to known test secrets with a wildcard loopback redirect, and creates a
legacy Baobei sales user in the shared realm.
"""
import os, secrets, importlib.util
HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('probe', os.path.join(HERE, 'keycloak-realm-probe.py'))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
call, admin_token, client_uuid = probe.call, probe.admin_token, probe.client_uuid
adm = admin_token()
out = {}
for realm in ('crane', 'baobei'):
    s, cl, _ = call('GET', f'/admin/realms/{realm}/clients?clientId=baobei-web', token=adm)
    secret = secrets.token_urlsafe(24)
    body = {'clientId': 'baobei-web', 'publicClient': False, 'secret': secret, 'standardFlowEnabled': True,
            'directAccessGrantsEnabled': False, 'redirectUris': ['http://127.0.0.1/*', 'http://127.0.0.1:*'], 'attributes': {'post.logout.redirect.uris': '+'}, 'fullScopeAllowed': False}
    if cl:
        cid = cl[0]['id']
        rep = cl[0]
        rep.update(body)
        call('PUT', f'/admin/realms/{realm}/clients/{cid}', rep, adm)
    else:
        call('POST', f'/admin/realms/{realm}/clients', body, adm)
        cid = client_uuid(adm, realm, 'baobei-web')
        for r in ('baobei_sales', 'baobei_manager', 'baobei_director', 'baobei_admin'):
            call('POST', f'/admin/realms/{realm}/clients/{cid}/roles', {'name': r}, adm)
    # Role claim mapper (resource_access.baobei-web.roles in the ID token) + role scope.
    s, mappers, _ = call('GET', f'/admin/realms/{realm}/clients/{cid}/protocol-mappers/models', token=adm)
    if not any(m['name'] == 'baobei roles' for m in mappers or []):
        call('POST', f'/admin/realms/{realm}/clients/{cid}/protocol-mappers/models', {
            'name': 'baobei roles', 'protocol': 'openid-connect', 'protocolMapper': 'oidc-usermodel-client-role-mapper',
            'config': {'usermodel.clientRoleMapping.clientId': 'baobei-web', 'claim.name': 'resource_access.${client_id}.roles',
                       'multivalued': 'true', 'jsonType.label': 'String', 'id.token.claim': 'true', 'access.token.claim': 'true'}}, adm)
    s, roles, _ = call('GET', f'/admin/realms/{realm}/clients/{cid}/roles', token=adm)
    call('POST', f'/admin/realms/{realm}/clients/{cid}/scope-mappings/clients/{cid}', roles, adm)
    out['BAOBEI_KC_%s_WEB_SECRET' % realm.upper()] = secret
# Legacy shared-realm user with the sales role.
name = 'legacy-drill-' + secrets.token_hex(3)
pw = 'Legacy-' + secrets.token_urlsafe(8)
s, _, h = call('POST', '/admin/realms/crane/users', {'username': name, 'enabled': True, 'email': name + '@example.test', 'emailVerified': True,
               'firstName': 'L', 'lastName': 'D', 'credentials': [{'type': 'password', 'value': pw, 'temporary': False}]}, adm)
uid = h['Location'].rsplit('/', 1)[-1]
cid = client_uuid(adm, 'crane', 'baobei-web')
s, role, _ = call('GET', f'/admin/realms/crane/clients/{cid}/roles/baobei_sales', token=adm)
call('POST', f'/admin/realms/crane/users/{uid}/role-mappings/clients/{cid}', [role], adm)
out.update({'BAOBEI_KC_LEGACY_USER': name, 'BAOBEI_KC_LEGACY_PASS': pw, 'BAOBEI_KC_LEGACY_UUID': uid,
            'BAOBEI_KC_NEW_PHONE': '139' + '%08d' % secrets.randbelow(10**8)})
for k, v in out.items():
    print(f'export {k}={v}')
