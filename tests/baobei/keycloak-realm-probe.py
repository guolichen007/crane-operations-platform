"""G4-S5-R1-E feasibility probe against a real Keycloak 26.8.1 (loopback, disposable H2).

Builds two realms: `crane` (stand-in for the shared realm, with a crane-web client and
a Crane admin user) and `baobei` (the dedicated realm). Creates the Baobei account
service client (client credentials, realm-management roles only in `baobei`) and checks
what the account service relies on. Prints one line per check; exits non-zero on failure.
Synthetic data only; the bootstrap admin is used solely to build the fixture.
"""
import json, os, secrets, sys, time, re, urllib.parse
from http.cookiejar import CookieJar
from urllib.error import HTTPError
from urllib.request import Request, build_opener, HTTPCookieProcessor, ProxyHandler, HTTPRedirectHandler

BASE = os.environ.get('KC', 'http://127.0.0.1:18080')
OPENER = build_opener(ProxyHandler({}))
results = []


def call(method, path, body=None, token=None, form=False, opener=OPENER, ok=(200, 201, 204)):
    headers = {}
    data = None
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if body is not None:
        headers['Content-Type'] = 'application/x-www-form-urlencoded' if form else 'application/json'
        data = (urllib.parse.urlencode(body) if form else json.dumps(body)).encode()
    req = Request(BASE + path if path.startswith('/') else path, data, headers, method=method)
    try:
        with opener.open(req, timeout=15) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw and r.headers.get_content_type() == 'application/json' else raw.decode(errors='replace')), r.headers
    except HTTPError as e:
        raw = e.read()
        try:
            payload = json.loads(raw)
        except Exception:
            payload = raw.decode(errors='replace')
        return e.code, payload, e.headers


def check(name, cond, detail=''):
    results.append((name, bool(cond)))
    print(('PASS ' if cond else 'FAIL ') + name + ((' :: ' + str(detail)[:300]) if detail and not cond else ''))


def token(realm, form):
    s, b, _ = call('POST', f'/realms/{realm}/protocol/openid-connect/token', form, form=True)
    return s, b


def admin_token():
    pw = open(os.path.expanduser('~/kc/bootstrap.pw')).read().strip()
    s, b = token('master', {'grant_type': 'password', 'client_id': 'admin-cli', 'username': 'fixture-bootstrap', 'password': pw})
    assert s == 200, b
    return b['access_token']


def ensure_realm(adm, realm, extra=None):
    s, _, _ = call('GET', f'/admin/realms/{realm}', token=adm)
    if s == 200:
        call('DELETE', f'/admin/realms/{realm}', token=adm)
    body = {'realm': realm, 'enabled': True}
    body.update(extra or {})
    s, b, _ = call('POST', '/admin/realms', body, adm)
    assert s == 201, (s, b)


def client_uuid(adm, realm, client_id):
    s, b, _ = call('GET', f'/admin/realms/{realm}/clients?clientId={client_id}', token=adm)
    return b[0]['id']


def browser_login(realm, client, redirect, username, password, new_password=None):
    """Drives the real Keycloak login form (Authorization Code). Returns (final_url_or_error, page)."""
    # Minimal cookie handling: Keycloak marks its login cookies Secure, which a
    # standard jar drops over loopback http; a real browser treats loopback as secure.
    jar = {}
    from urllib.request import BaseHandler

    class Cookies(BaseHandler):
        def http_request(self, req):
            if jar:
                req.add_unredirected_header('Cookie', '; '.join(f'{k}={v}' for k, v in jar.items()))
            return req

        def http_response(self, req, resp):
            for raw in resp.headers.get_all('Set-Cookie') or []:
                k, _, rest = raw.partition('=')
                v = rest.split(';', 1)[0]
                if 'Max-Age=0' in raw or 'Expires=Thu, 01-Jan-1970' in raw:
                    jar.pop(k.strip(), None)
                else:
                    jar[k.strip()] = v
            return resp
        https_request, https_response = http_request, http_response

    class NoRedirect(HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None
    op = build_opener(ProxyHandler({}), Cookies(), NoRedirect)
    q = urllib.parse.urlencode({'client_id': client, 'redirect_uri': redirect, 'response_type': 'code', 'scope': 'openid', 'prompt': 'login'})
    s, page, h = call('GET', f'/realms/{realm}/protocol/openid-connect/auth?{q}', opener=op)
    for _ in range(6):
        while s in (302, 303):
            loc = h['Location']
            if loc.startswith(redirect):
                return loc, ''
            s, page, h = call('GET', loc, opener=op)
        m = re.search(r'action="([^"]+)"', page)
        if not m:
            open('/tmp/kc-noform.html', 'w').write(page)
            return 'NO_FORM', page
        action = m.group(1).replace('&amp;', '&')
        if 'password-new' in page:
            form = {'password-new': new_password, 'password-confirm': new_password}
        else:
            form = {'username': username, 'password': password, 'credentialId': ''}
        s, page, h = call('POST', action, form, form=True, opener=op)
        if s == 200 and ('password-new' not in page) and 'kc-form-login' in page:
            return 'LOGIN_FAILED', page
    return 'STUCK', page


def main():
    adm = admin_token()
    # ---- fixture: crane realm with a Crane user and client
    ensure_realm(adm, 'crane')
    call('POST', '/admin/realms/crane/clients', {'clientId': 'crane-web', 'publicClient': True, 'redirectUris': ['http://127.0.0.1/crane/*']}, adm)
    call('POST', '/admin/realms/crane/clients/' + client_uuid(adm, 'crane', 'crane-web') + '/roles', {'name': 'crane_admin'}, adm)
    crane_pw = secrets.token_urlsafe(18)  # per-run synthetic value
    s, _, h = call('POST', '/admin/realms/crane/users', {'username': 'crane-ops', 'enabled': True, 'credentials': [{'type': 'password', 'value': crane_pw, 'temporary': False}]}, adm)
    crane_user = h['Location'].rsplit('/', 1)[-1]

    # ---- baobei realm: phone username, no email required, default UPDATE_PASSWORD on reset only
    ensure_realm(adm, 'baobei', {'registrationAllowed': False, 'resetPasswordAllowed': False, 'loginWithEmailAllowed': False,
                                  'duplicateEmailsAllowed': True, 'editUsernameAllowed': True, 'rememberMe': False,
                                  'internationalizationEnabled': True, 'supportedLocales': ['zh-Hans', 'zh-CN'], 'defaultLocale': 'zh-Hans',
                                  'loginTheme': os.environ.get('BAOBEI_LOGIN_THEME', 'keycloak.v2')})
    # User profile: email optional, no firstName/lastName prompts.
    s, up, _ = call('GET', '/admin/realms/baobei/users/profile', token=adm)
    up['attributes'] = [a for a in up['attributes'] if a['name'] in ('username', 'email')]
    # Admin-only link attributes (26.8 drops undeclared attributes silently).
    for name in ('baobei_staff_id', 'baobei_subject'):
        up['attributes'].append({'name': name, 'displayName': name, 'multivalued': False,
                                 'permissions': {'view': ['admin'], 'edit': ['admin']}, 'validations': {}})
    for a in up['attributes']:
        if a['name'] == 'email':
            a.pop('required', None)
            a['permissions'] = {'view': ['admin'], 'edit': ['admin']}
        if a['name'] == 'username':
            a['permissions'] = {'view': ['admin', 'user'], 'edit': ['admin']}
    s, b, _ = call('PUT', '/admin/realms/baobei/users/profile', up, adm)
    check('user profile: email optional, username admin-only', s == 200, b)

    call('POST', '/admin/realms/baobei/clients', {'clientId': 'baobei-web', 'publicClient': False, 'secret': secrets.token_urlsafe(24),
         'standardFlowEnabled': True, 'directAccessGrantsEnabled': False, 'redirectUris': ['http://127.0.0.1/cb'], 'fullScopeAllowed': False}, adm)
    web = client_uuid(adm, 'baobei', 'baobei-web')
    for r in ('baobei_sales', 'baobei_manager', 'baobei_director', 'baobei_admin'):
        call('POST', f'/admin/realms/baobei/clients/{web}/roles', {'name': r}, adm)
    # Account service: confidential, service account only.
    svc_secret = secrets.token_urlsafe(24)
    call('POST', '/admin/realms/baobei/clients', {'clientId': 'baobei-account-service', 'publicClient': False, 'secret': svc_secret,
         'serviceAccountsEnabled': True, 'standardFlowEnabled': False, 'directAccessGrantsEnabled': False, 'fullScopeAllowed': False}, adm)
    svc = client_uuid(adm, 'baobei', 'baobei-account-service')
    s, sa, _ = call('GET', f'/admin/realms/baobei/clients/{svc}/service-account-user', token=adm)
    rm = client_uuid(adm, 'baobei', 'realm-management')
    s, rmroles, _ = call('GET', f'/admin/realms/baobei/clients/{rm}/roles', token=adm)
    grant = [r for r in rmroles if r['name'] in ('manage-users', 'view-users', 'query-users', 'view-clients', 'query-clients')]
    s, b, _ = call('POST', f'/admin/realms/baobei/users/{sa["id"]}/role-mappings/clients/{rm}', grant, adm)
    check('service account granted manage-users in baobei realm only', s == 204, b)
    # fullScopeAllowed=false: the token carries only the roles also in the client's scope.
    s, b, _ = call('POST', f'/admin/realms/baobei/clients/{svc}/scope-mappings/clients/{rm}', grant, adm)
    check('service client scope limited to those realm-management roles', s == 204, b)

    s, b = token('baobei', {'grant_type': 'client_credentials', 'client_id': 'baobei-account-service', 'client_secret': svc_secret})
    check('account service obtains token (client credentials)', s == 200, b)
    svc_tok = b['access_token']

    # ---- account service capabilities in its own realm
    phone = '138' + str(secrets.randbelow(10**8)).zfill(8)
    s, b, h = call('POST', '/admin/realms/baobei/users', {'username': phone, 'enabled': False, 'emailVerified': False,
                   'attributes': {'baobei_staff_id': ['00000000-0000-4000-8000-000000000001']}}, svc_tok)
    check('create disabled user with 11-digit phone username, no email', s == 201, b)
    uid = h['Location'].rsplit('/', 1)[-1]
    s, b, _ = call('POST', '/admin/realms/baobei/users', {'username': phone, 'enabled': False}, svc_tok)
    check('duplicate phone username refused (409)', s == 409, s)
    temp = 'Tmp-' + secrets.token_urlsafe(9)
    s, b, _ = call('PUT', f'/admin/realms/baobei/users/{uid}/reset-password', {'type': 'password', 'value': temp, 'temporary': True}, svc_tok)
    check('set temporary password', s == 204, b)
    s, roles, _ = call('GET', f'/admin/realms/baobei/clients/{web}/roles/baobei_sales', token=svc_tok)
    s, b, _ = call('POST', f'/admin/realms/baobei/users/{uid}/role-mappings/clients/{web}', [roles], svc_tok)
    check('assign exact baobei-web client role', s == 204, b)
    s, b, _ = call('PUT', f'/admin/realms/baobei/users/{uid}', {'enabled': True}, svc_tok)
    check('enable after binding', s == 204, b)
    s, u, _ = call('GET', f'/admin/realms/baobei/users/{uid}', token=svc_tok)
    check('temporary password puts UPDATE_PASSWORD as required action', 'UPDATE_PASSWORD' in u.get('requiredActions', []), u.get('requiredActions'))

    # ---- real login form: 11-digit phone + temporary password → forced new password, no email prompt
    newpw = 'New-' + secrets.token_urlsafe(10)
    loc, page = browser_login('baobei', 'baobei-web', 'http://127.0.0.1/cb', phone, temp, newpw)
    check('11-digit phone + temp password login forces UPDATE_PASSWORD then succeeds', 'code=' in loc, loc + ' ' + re.sub(r'\s+', ' ', page)[:200])
    s, u, _ = call('GET', f'/admin/realms/baobei/users/{uid}', token=svc_tok)
    check('no remaining required actions (no email/profile prompt)', u.get('requiredActions') == [], u.get('requiredActions'))
    loc, _ = browser_login('baobei', 'baobei-web', 'http://127.0.0.1/cb', phone, newpw)
    check('later login with phone + new password', 'code=' in loc, loc)
    loc, _ = browser_login('baobei', 'baobei-web', 'http://127.0.0.1/cb', '+86' + phone, newpw)
    check('+86-prefixed input is NOT accepted as the same login (username is the 11 digits)', 'code=' not in loc, loc)

    # ---- change phone: UUID kept, old login name dead, new works
    phone2 = '139' + str(secrets.randbelow(10**8)).zfill(8)
    s, b, _ = call('PUT', f'/admin/realms/baobei/users/{uid}', {'username': phone2}, svc_tok)
    check('account service can change the login phone (username)', s == 204, b)
    s, u, _ = call('GET', f'/admin/realms/baobei/users/{uid}', token=svc_tok)
    check('UUID unchanged after phone change', u['id'] == uid and u['username'] == phone2, u.get('username'))
    loc, _ = browser_login('baobei', 'baobei-web', 'http://127.0.0.1/cb', phone, newpw)
    check('old phone no longer logs in', 'code=' not in loc, loc)
    loc, _ = browser_login('baobei', 'baobei-web', 'http://127.0.0.1/cb', phone2, newpw)
    check('new phone logs in', 'code=' in loc, loc)

    # ---- reset password: new temp, cannot read old
    s, b, _ = call('PUT', f'/admin/realms/baobei/users/{uid}/reset-password', {'type': 'password', 'value': 'Tmp-' + secrets.token_urlsafe(9), 'temporary': True}, svc_tok)
    check('reset password sets new temporary', s == 204, b)
    s, creds, _ = call('GET', f'/admin/realms/baobei/users/{uid}/credentials', token=svc_tok)
    check('credential listing exposes no secret value', s == 200 and all('value' not in c and 'secretData' not in c for c in creds), creds)
    loc, _ = browser_login('baobei', 'baobei-web', 'http://127.0.0.1/cb', phone2, newpw)
    check('old password rejected after reset', 'code=' not in loc, loc)

    # ---- suspend: disabled user cannot log in; sessions removable
    s, b, _ = call('PUT', f'/admin/realms/baobei/users/{uid}', {'enabled': False}, svc_tok)
    s2, b2, _ = call('POST', f'/admin/realms/baobei/users/{uid}/logout', None, svc_tok)
    check('disable + logout user sessions', s == 204 and s2 == 204, (b, b2))

    # ---- the user cannot change their own login phone: the user-profile attribute
    # 'username' is edit=['admin'] only. Prove it through the real account REST API
    # with the user's own token (a test-only public client with direct grants).
    call('POST', '/admin/realms/baobei/clients', {'clientId': 'probe-self', 'publicClient': True, 'directAccessGrantsEnabled': True,
         'standardFlowEnabled': False, 'fullScopeAllowed': True}, adm)
    s, b, _ = call('PUT', f'/admin/realms/baobei/users/{uid}', {'enabled': True}, svc_tok)
    selfpw = 'Self-' + secrets.token_urlsafe(10)
    call('PUT', f'/admin/realms/baobei/users/{uid}/reset-password', {'type': 'password', 'value': selfpw, 'temporary': False}, svc_tok)
    s, ut = token('baobei', {'grant_type': 'password', 'client_id': 'probe-self', 'username': phone2, 'password': selfpw, 'scope': 'openid'})
    if s == 200:
        s, me, _ = call('GET', '/realms/baobei/account/', token=ut['access_token'])
        me = me if isinstance(me, dict) else {}
        me['username'] = '13' + str(secrets.randbelow(10**9)).zfill(9)
        s, b, _ = call('POST', '/realms/baobei/account/', me, ut['access_token'])
        s2, u2, _ = call('GET', f'/admin/realms/baobei/users/{uid}', token=svc_tok)
        check('user cannot change own login phone via account API', u2['username'] == phone2, (s, b, u2.get('username')))
    else:
        check('user token for self-service probe', False, ut)
    call('DELETE', '/admin/realms/baobei/clients/' + client_uuid(adm, 'baobei', 'probe-self'), token=adm)
    call('PUT', f'/admin/realms/baobei/users/{uid}', {'enabled': False}, svc_tok)

    # ---- isolation: the account service cannot touch the crane realm or escalate
    s, b, _ = call('GET', '/admin/realms/crane/users', token=svc_tok)
    check('crane realm users: list denied', s in (401, 403), s)
    s, b, _ = call('GET', f'/admin/realms/crane/users/{crane_user}', token=svc_tok)
    check('crane realm user: read denied', s in (401, 403), s)
    s, b, _ = call('PUT', f'/admin/realms/crane/users/{crane_user}/reset-password', {'type': 'password', 'value': 'x', 'temporary': True}, svc_tok)
    check('crane realm user: password reset denied', s in (401, 403), s)
    s, b, _ = call('POST', '/admin/realms/crane/users', {'username': 'intruder', 'enabled': True}, svc_tok)
    check('crane realm: create user denied', s in (401, 403), s)
    s, b, _ = call('GET', '/admin/realms/master/users', token=svc_tok)
    check('master realm: denied', s in (401, 403), s)
    s, b, _ = call('PUT', '/admin/realms/baobei', {'registrationAllowed': True}, svc_tok)
    check('baobei realm settings: change denied', s in (401, 403), s)
    s, rmr, _ = call('GET', f'/admin/realms/baobei/clients/{rm}/roles/realm-admin', token=adm)
    s, b, _ = call('POST', f'/admin/realms/baobei/users/{sa["id"]}/role-mappings/clients/{rm}', [rmr], svc_tok)
    check('self-grant realm-admin denied', s in (401, 403), s)
    s, b, h = call('POST', '/admin/realms/baobei/users', {'username': '137' + str(secrets.randbelow(10**8)).zfill(8), 'enabled': False}, svc_tok)
    other = h.get('Location', '/x').rsplit('/', 1)[-1]
    s, b, _ = call('POST', f'/admin/realms/baobei/users/{other}/role-mappings/clients/{rm}', [rmr], svc_tok)
    check('grant realm-admin to another user denied', s in (401, 403), s)
    # A crane-realm token is not accepted by the baobei realm admin API.
    s, ct = token('crane', {'grant_type': 'password', 'client_id': 'admin-cli', 'username': 'crane-ops', 'password': crane_pw})
    if s == 200:
        s, b, _ = call('GET', '/admin/realms/baobei/users', token=ct['access_token'])
        check('crane-realm token rejected by baobei admin API', s in (401, 403), s)
    out = os.environ.get('BAOBEI_KEYCLOAK_SECRET_OUT')
    if out:
        # Hand the account service secret to the Go adapter test (loopback fixture only).
        with open(out, 'w') as fh:
            fh.write(svc_secret)
        os.chmod(out, 0o600)
    print(json.dumps({'passed': sum(1 for _, ok in results if ok), 'failed': [n for n, ok in results if not ok]}, ensure_ascii=False))
    sys.exit(0 if all(ok for _, ok in results) else 1)


if __name__ == '__main__':
    main()
