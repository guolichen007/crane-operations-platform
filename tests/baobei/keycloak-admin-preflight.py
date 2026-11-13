"""G5-RC0-R1 drill: admin-preflight.py against a real Keycloak 26.8.1 (loopback, disposable).

Builds the dedicated `baobei` realm like the R1-E probe (baobei-web with the four
client roles, baobei-account-service with read/manage-users only), then a
takeover administrator (11-digit phone username, baobei_staff_id / baobei_subject,
exact baobei_admin), and runs the preflight: account checks and a real login.
Negative cases: wrong role, extra role, disabled, staff id mismatch, link to
another subject, pending UPDATE_PASSWORD, wrong password. Synthetic data only;
nothing is printed but PASS/FAIL lines and masked JSON.
"""
import importlib.util
import json
import os
import secrets
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('probe', os.path.join(HERE, 'keycloak-realm-probe.py'))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
TOOL = os.path.join(HERE, '..', '..', 'deploy', 'baobei', 'admin-preflight.py')
BASE = probe.BASE
REDIRECT = 'http://127.0.0.1/cb'


def private_file(tmp, name, text):
    path = os.path.join(tmp, name)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as fh:
        fh.write(text)
    return path


def run(tmp, mode, admin, link, svc, pw=None, web=None):
    args = [sys.executable, '-B', TOOL, mode, '--base', BASE, '--realm', 'baobei', '--admin', admin, '--link', link,
            '--service-secret-file', svc]
    if mode == 'login':
        args += ['--password-file', pw, '--web-secret-file', web, '--redirect', REDIRECT]
    r = subprocess.run(args, capture_output=True, text=True)
    return r.returncode, r.stdout, r.stderr


def main():
    adm = probe.admin_token()
    call, check = probe.call, probe.check
    probe.ensure_realm(adm, 'baobei', {'registrationAllowed': False, 'resetPasswordAllowed': False,
                                       'loginWithEmailAllowed': False, 'editUsernameAllowed': True})
    s, up, _ = call('GET', '/admin/realms/baobei/users/profile', token=adm)
    up['attributes'] = [a for a in up['attributes'] if a['name'] in ('username', 'email')]
    for name in ('baobei_staff_id', 'baobei_subject'):
        up['attributes'].append({'name': name, 'displayName': name, 'multivalued': False,
                                 'permissions': {'view': ['admin'], 'edit': ['admin']}, 'validations': {}})
    for a in up['attributes']:
        if a['name'] == 'email':
            a.pop('required', None)
    call('PUT', '/admin/realms/baobei/users/profile', up, adm)
    web_secret, svc_secret = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    call('POST', '/admin/realms/baobei/clients', {'clientId': 'baobei-web', 'publicClient': False, 'secret': web_secret,
         'standardFlowEnabled': True, 'directAccessGrantsEnabled': False, 'redirectUris': [REDIRECT], 'fullScopeAllowed': False,
         'attributes': {'pkce.code.challenge.method': 'S256'}}, adm)
    web = probe.client_uuid(adm, 'baobei', 'baobei-web')
    for r in ('baobei_sales', 'baobei_manager', 'baobei_director', 'baobei_admin'):
        call('POST', f'/admin/realms/baobei/clients/{web}/roles', {'name': r}, adm)
    # Production contract (Keycloak生产配置契约.md): resource_access.baobei-web.roles
    # in the ID token via a client-role mapper, plus role scope mappings.
    call('POST', f'/admin/realms/baobei/clients/{web}/protocol-mappers/models', {
        'name': 'baobei roles', 'protocol': 'openid-connect', 'protocolMapper': 'oidc-usermodel-client-role-mapper',
        'config': {'usermodel.clientRoleMapping.clientId': 'baobei-web', 'claim.name': 'resource_access.${client_id}.roles',
                   'multivalued': 'true', 'jsonType.label': 'String', 'id.token.claim': 'true', 'access.token.claim': 'true'}}, adm)
    # Roles of baobei-web must reach the token (client scope of its own roles).
    s, own, _ = call('GET', f'/admin/realms/baobei/clients/{web}/roles', token=adm)
    call('POST', f'/admin/realms/baobei/clients/{web}/scope-mappings/clients/{web}', own, adm)
    call('POST', '/admin/realms/baobei/clients', {'clientId': 'baobei-account-service', 'publicClient': False, 'secret': svc_secret,
         'serviceAccountsEnabled': True, 'standardFlowEnabled': False, 'directAccessGrantsEnabled': False, 'fullScopeAllowed': False}, adm)
    svc = probe.client_uuid(adm, 'baobei', 'baobei-account-service')
    s, sa, _ = call('GET', f'/admin/realms/baobei/clients/{svc}/service-account-user', token=adm)
    rm = probe.client_uuid(adm, 'baobei', 'realm-management')
    s, rmroles, _ = call('GET', f'/admin/realms/baobei/clients/{rm}/roles', token=adm)
    grant = [r for r in rmroles if r['name'] in ('manage-users', 'view-users', 'query-users', 'view-clients', 'query-clients')]
    call('POST', f'/admin/realms/baobei/users/{sa["id"]}/role-mappings/clients/{rm}', grant, adm)
    call('POST', f'/admin/realms/baobei/clients/{svc}/scope-mappings/clients/{rm}', grant, adm)

    tmp = tempfile.mkdtemp(prefix='admin-preflight-')
    os.chmod(tmp, 0o700)
    staff_id, subject = '%08x-1111-4000-8000-%012x' % (secrets.randbits(32), 1), '%08x-2222-4000-8000-%012x' % (secrets.randbits(32), 1)
    phone = '+86139' + str(secrets.randbelow(10**8)).zfill(8)
    password = 'Adm-' + secrets.token_urlsafe(12)
    s, _, h = call('POST', '/admin/realms/baobei/users', {'username': phone[3:], 'enabled': True, 'emailVerified': False,
                   'attributes': {'baobei_staff_id': [staff_id], 'baobei_subject': [subject]},
                   'credentials': [{'type': 'password', 'value': password, 'temporary': False}]}, adm)
    ext = h['Location'].rsplit('/', 1)[-1]
    s, role, _ = call('GET', f'/admin/realms/baobei/clients/{web}/roles/baobei_admin', token=adm)
    call('POST', f'/admin/realms/baobei/users/{ext}/role-mappings/clients/{web}', [role], adm)
    issuer = BASE + '/realms/baobei'
    n = [0]

    def files(admin_row=None, link_row=None, pw=password):
        n[0] += 1
        a = private_file(tmp, f'admin-{n[0]}.json', json.dumps(admin_row or {'staff_id': staff_id, 'subject': subject, 'role': 'baobei_admin',
                                                                            'staff_status': 'ACTIVE', 'work_phone': phone}))
        lk = private_file(tmp, f'link-{n[0]}.json', json.dumps(link_row or {'issuer': issuer, 'external_subject': ext, 'subject': subject}))
        p = private_file(tmp, f'pw-{n[0]}', pw)
        return a, lk, p
    svc_file = private_file(tmp, 'svc', svc_secret)
    web_file = private_file(tmp, 'web', web_secret)

    def verdict(out):
        try:
            return json.loads(out)
        except Exception:
            return {}

    a, lk, p = files()
    code, out, err = run(tmp, 'login', a, lk, svc_file, p, web_file)
    v = verdict(out)
    check('ready administrator: account checks PASS', v.get('ADMIN_ACCOUNT_VERIFIED') == 'PASS', out + err)
    check('ready administrator: real login through the target realm PASS (token sub = link, exactly baobei_admin)',
          code == 0 and v.get('ADMIN_LOGIN_VERIFIED') == 'PASS', out + err)
    check('output never contains the phone, the password or the secrets',
          all(x not in out + err for x in (phone, phone[3:], password, svc_secret, web_secret)), 'leak')
    code, out, err = run(tmp, 'admin', a, lk, svc_file)
    v = verdict(out)
    check('admin-only mode never claims a login (ADMIN_LOGIN_VERIFIED=NOT_RUN)',
          code == 0 and v.get('ADMIN_LOGIN_VERIFIED') == 'NOT_RUN', out + err)

    a, lk, p = files(pw='wrong-' + secrets.token_urlsafe(8))
    code, out, err = run(tmp, 'login', a, lk, svc_file, p, web_file)
    check('wrong password → ADMIN_LOGIN_VERIFIED FAIL, exit 1', code == 1 and verdict(out).get('ADMIN_LOGIN_VERIFIED') == 'FAIL', out + err)

    a, lk, p = files(link_row={'issuer': issuer, 'external_subject': ext, 'subject': '%08x-3333-4000-8000-%012x' % (1, 1)})
    code, out, err = run(tmp, 'login', a, lk, svc_file, p, web_file)
    check('link to another business subject → account FAIL, no login attempted',
          code == 1 and verdict(out).get('ADMIN_ACCOUNT_VERIFIED') == 'FAIL' and verdict(out).get('ADMIN_LOGIN_VERIFIED') == 'NOT_RUN', out + err)

    a, lk, p = files(admin_row={'staff_id': '%08x-1111-4000-8000-%012x' % (2, 2), 'subject': subject, 'role': 'baobei_admin',
                                'staff_status': 'ACTIVE', 'work_phone': phone})
    code, out, err = run(tmp, 'admin', a, lk, svc_file)
    check('staff id attribute mismatch → FAIL', code == 1 and 'baobei_staff_id' in out, out + err)

    s, sales, _ = call('GET', f'/admin/realms/baobei/clients/{web}/roles/baobei_sales', token=adm)
    call('POST', f'/admin/realms/baobei/users/{ext}/role-mappings/clients/{web}', [sales], adm)
    a, lk, p = files()
    code, out, err = run(tmp, 'admin', a, lk, svc_file)
    check('extra baobei-web role → FAIL (role must be exactly baobei_admin)', code == 1 and 'baobei_admin' in out, out + err)
    call('DELETE', f'/admin/realms/baobei/users/{ext}/role-mappings/clients/{web}', [sales], adm)

    call('PUT', f'/admin/realms/baobei/users/{ext}', {'enabled': False}, adm)
    code, out, err = run(tmp, 'login', a, lk, svc_file, p, web_file)
    check('disabled administrator → FAIL', code == 1 and verdict(out).get('ADMIN_ACCOUNT_VERIFIED') == 'FAIL', out + err)
    call('PUT', f'/admin/realms/baobei/users/{ext}', {'enabled': True, 'requiredActions': ['UPDATE_PASSWORD']}, adm)
    a, lk, p = files()
    code, out, err = run(tmp, 'login', a, lk, svc_file, p, web_file)
    check('pending UPDATE_PASSWORD → not ready (login FAIL, never auto-completed)',
          code == 1 and verdict(out).get('ADMIN_LOGIN_VERIFIED') == 'FAIL', out + err)
    s, u, _ = call('GET', f'/admin/realms/baobei/users/{ext}', token=adm)
    check('preflight changed nothing in Keycloak (required action still pending, enabled unchanged)',
          u.get('requiredActions') == ['UPDATE_PASSWORD'] and u.get('enabled') is True, u)

    a, lk, p = files(link_row={'issuer': issuer, 'external_subject': '%08x-4444-4000-8000-%012x' % (3, 3), 'subject': subject})
    code, out, err = run(tmp, 'admin', a, lk, svc_file)
    check('link to a Keycloak user that does not exist → FAIL', code == 1 and '不存在' in out, out + err)

    # Without the ID-token role mapper the preflight must fail (roles only in the
    # access token do not satisfy the contract).
    call('PUT', f'/admin/realms/baobei/users/{ext}', {'enabled': True, 'requiredActions': []}, adm)
    s, mappers, _ = call('GET', f'/admin/realms/baobei/clients/{web}/protocol-mappers/models', token=adm)
    for m in mappers:
        if m['name'] == 'baobei roles':
            call('DELETE', f'/admin/realms/baobei/clients/{web}/protocol-mappers/models/{m["id"]}', token=adm)
    a, lk, p = files()
    code, out, err = run(tmp, 'login', a, lk, svc_file, p, web_file)
    check('ID token without resource_access.baobei-web.roles → login FAIL (contract)',
          code == 1 and verdict(out).get('ADMIN_LOGIN_VERIFIED') == 'FAIL', out + err)

    failed = [n for n, ok in probe.results if not ok]
    print(('ADMIN_PREFLIGHT_DRILL=PASS %d/%d' % (len(probe.results), len(probe.results))) if not failed else
          ('ADMIN_PREFLIGHT_DRILL=FAIL ' + '; '.join(failed)))
    sys.exit(1 if failed else 0)


if __name__ == '__main__':
    main()
