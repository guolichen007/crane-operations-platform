#!/usr/bin/env python3
"""Baobei takeover-administrator preflight against the target Keycloak realm.

READ-ONLY on Keycloak and the database export; never creates, enables or changes
any account. Run by the release operator before IDENTITY_PRECHECK. Two parts:

  admin   (account service credentials, read-only Admin API calls)
          - the Keycloak user for the takeover administrator exists in the realm
            and is enabled, carries baobei_staff_id / baobei_subject matching the
            business row, username = the 11-digit approved work phone;
          - client roles on baobei-web are exactly ["baobei_admin"]; no other
            baobei-web role, no realm-management role;
          - the identity link (issuer, external_subject) maps to this business
            subject and the Keycloak user id equals external_subject.
  login   real sign-in by the administrator through the target realm's login
          form (Authorization Code + PKCE, prompt=login). The password is read
          from a private file (0600) supplied by the administrator, used once,
          never printed. The resulting ID token is verified (issuer, audience,
          nonce, signature via the realm JWKS) and must carry sub =
          external_subject and exactly baobei_admin in resource_access.

Output: JSON with masked values only. ADMIN_ACCOUNT_VERIFIED is PASS only when
every admin check passed; ADMIN_LOGIN_VERIFIED is PASS only when the real login
succeeded and the token matched. Anything not run is NOT_RUN, never PASS.

Inputs:
  --base         Keycloak base URL (https in production; http only for 127.0.0.1)
  --realm        approved realm (baobei)
  --admin        business row of the takeover admin: private JSON file (0600)
                 {"staff_id", "subject", "role", "staff_status", "work_phone"}
  --link         identity link row: {"issuer", "external_subject", "subject"}
  --service-secret-file   account-service client secret (0600)
  --password-file         administrator's own password (0600), only for `login`
  --redirect     registered redirect URI of baobei-web (login only)
  --web-secret-file       baobei-web client secret (0600, login only)
"""
import argparse
import base64
import hashlib
import json
import os
import re
import secrets
import stat
import sys
import urllib.parse
from urllib.error import HTTPError
from urllib.request import BaseHandler, HTTPRedirectHandler, ProxyHandler, Request, build_opener

UUID = re.compile(r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')
MAINLAND = re.compile(r'^\+861[3-9]\d{9}$')
APPROVED = {'baobei': {'client': 'baobei-web', 'service': 'baobei-account-service'}}


class Refused(Exception):
    pass


def private(path):
    if os.name == 'posix' and os.stat(path).st_mode & (stat.S_IRWXG | stat.S_IRWXO):
        raise Refused('%s 必须是仅本人可读的私有文件（chmod 600）' % os.path.basename(path))
    with open(path, encoding='utf-8') as fh:
        return fh.read().strip()


def mask(phone):
    return phone[:3] + '****' + phone[-4:] if len(phone) >= 7 else '****'


class Client:
    def __init__(self, base):
        u = urllib.parse.urlparse(base)
        if not (u.scheme == 'https' or (u.scheme == 'http' and u.hostname in ('127.0.0.1', 'localhost'))):
            raise Refused('Keycloak 地址必须为 https（仅本机回环允许 http）')
        self.base = base.rstrip('/')
        self.jar = {}
        jar = self.jar

        class Cookies(BaseHandler):
            def http_request(self, req):
                if jar:
                    req.add_unredirected_header('Cookie', '; '.join(f'{k}={v}' for k, v in jar.items()))
                return req

            def http_response(self, req, resp):
                for raw in resp.headers.get_all('Set-Cookie') or []:
                    k, _, rest = raw.partition('=')
                    jar[k.strip()] = rest.split(';', 1)[0]
                return resp
            https_request, https_response = http_request, http_response

        class NoRedirect(HTTPRedirectHandler):
            def redirect_request(self, *a, **k):
                return None
        self.op = build_opener(ProxyHandler({}), Cookies(), NoRedirect)

    def call(self, method, path, body=None, token=None, form=False):
        headers, data = {}, None
        if token:
            headers['Authorization'] = 'Bearer ' + token
        if body is not None:
            headers['Content-Type'] = 'application/x-www-form-urlencoded' if form else 'application/json'
            data = (urllib.parse.urlencode(body) if form else json.dumps(body)).encode()
        url = path if path.startswith('http') else self.base + path
        try:
            with self.op.open(Request(url, data, headers, method=method), timeout=15) as r:
                raw = r.read()
                ctype = r.headers.get_content_type()
                return r.status, (json.loads(raw) if raw and ctype == 'application/json' else raw.decode(errors='replace')), r.headers
        except HTTPError as e:
            return e.code, e.read().decode(errors='replace'), e.headers


def b64(data):
    return base64.urlsafe_b64decode(data + '=' * (-len(data) % 4))


def verify_id_token(c, realm, client, token, nonce):
    """Signature (RS256 via realm JWKS), issuer, audience, nonce, expiry."""
    head, body, sig = token.split('.')
    header, claims = json.loads(b64(head)), json.loads(b64(body))
    s, jwks, _ = c.call('GET', f'/realms/{realm}/protocol/openid-connect/certs')
    key = next((k for k in jwks.get('keys', []) if k.get('kid') == header.get('kid')), None)
    if header.get('alg') != 'RS256' or not key:
        return None, 'unsupported token signature'
    n, e = int.from_bytes(b64(key['n']), 'big'), int.from_bytes(b64(key['e']), 'big')
    em = pow(int.from_bytes(b64(sig), 'big'), e, n).to_bytes((n.bit_length() + 7) // 8, 'big')
    digest = hashlib.sha256((head + '.' + body).encode()).digest()
    prefix = bytes.fromhex('3031300d060960864801650304020105000420')
    if not em.endswith(prefix + digest) or not em.startswith(b'\x00\x01\xff'):
        return None, 'signature invalid'
    import time
    if (claims.get('iss') != c.base + '/realms/' + realm or claims.get('nonce') != nonce or
            client not in ([claims.get('aud')] if isinstance(claims.get('aud'), str) else claims.get('aud', [])) or
            claims.get('exp', 0) < time.time()):
        return None, 'issuer/audience/nonce/expiry mismatch'
    return claims, ''


def check_admin(args, report):
    c = Client(args.base)
    realm, approved = args.realm, APPROVED.get(args.realm)
    if not approved:
        raise Refused('Realm 不在批准名单')
    admin = json.loads(private(args.admin))
    link = json.loads(private(args.link))
    issuer = c.base + '/realms/' + realm
    problems = []
    if not (UUID.match(admin.get('staff_id', '')) and admin.get('role') == 'baobei_admin' and admin.get('staff_status') == 'ACTIVE'):
        problems.append('业务记录不是在用的 baobei_admin')
    if not MAINLAND.match(admin.get('work_phone', '')):
        problems.append('业务记录没有大陆工作手机（不得猜测）')
    if link.get('issuer') != issuer or link.get('subject') != admin.get('subject') or not UUID.match(link.get('external_subject', '')):
        problems.append('身份链接与业务 subject 或目标 Issuer 不一致')
    s, tok, _ = c.call('POST', f'/realms/{realm}/protocol/openid-connect/token',
                       {'grant_type': 'client_credentials', 'client_id': approved['service'],
                        'client_secret': private(args.service_secret_file)}, form=True)
    if s != 200:
        raise Refused('账号服务凭据无法取得令牌（%s）' % s)
    t = tok['access_token']
    ext = link.get('external_subject', '')
    s, u, _ = c.call('GET', f'/admin/realms/{realm}/users/{urllib.parse.quote(ext)}', token=t)
    if s != 200:
        problems.append('目标 Realm 中不存在该管理员账号')
        u = {}
    attrs = u.get('attributes') or {}
    if u:
        if not u.get('enabled'):
            problems.append('管理员账号未启用')
        if u.get('id') != ext:
            problems.append('Keycloak 用户 ID 与身份链接不一致')
        if attrs.get('baobei_staff_id') != [admin.get('staff_id')]:
            problems.append('baobei_staff_id 属性与业务记录不一致')
        if attrs.get('baobei_subject') not in (None, [admin.get('subject')]):
            problems.append('baobei_subject 属性与业务 subject 不一致')
        if u.get('username') != admin.get('work_phone', '')[3:]:
            problems.append('登录名不是批准的工作手机号')
        s, clients, _ = c.call('GET', f'/admin/realms/{realm}/clients?clientId={approved["client"]}', token=t)
        web = clients[0]['id'] if s == 200 and clients else None
        s, mapped, _ = c.call('GET', f'/admin/realms/{realm}/users/{ext}/role-mappings', token=t)
        client_maps = (mapped or {}).get('clientMappings') or {} if s == 200 else {}
        web_roles = sorted(r['name'] for r in (client_maps.get(approved['client']) or {}).get('mappings', []))
        if not web or web_roles != ['baobei_admin']:
            problems.append('baobei-web Client Role 不是精确的 baobei_admin（实际 %s）' % web_roles)
        if 'realm-management' in client_maps:
            problems.append('管理员账号不应持有 realm-management 角色')
    report['admin'] = {'staffId': admin.get('staff_id'), 'login': mask(admin.get('work_phone', '')),
                       'keycloakUser': ext[:8] + '…', 'problems': problems}
    report['ADMIN_ACCOUNT_VERIFIED'] = 'PASS' if not problems else 'FAIL'
    return c, admin, link


def check_login(args, report, c, admin, link):
    realm, approved = args.realm, APPROVED[args.realm]
    verifier = secrets.token_urlsafe(48)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
    nonce, st = secrets.token_urlsafe(16), secrets.token_urlsafe(16)
    q = urllib.parse.urlencode({'client_id': approved['client'], 'redirect_uri': args.redirect, 'response_type': 'code',
                                'scope': 'openid', 'prompt': 'login', 'nonce': nonce, 'state': st,
                                'code_challenge': challenge, 'code_challenge_method': 'S256'})
    s, page, h = c.call('GET', f'/realms/{realm}/protocol/openid-connect/auth?{q}')
    password = private(args.password_file)
    code = None
    for _ in range(4):
        while s in (302, 303):
            loc = h['Location']
            if loc.startswith(args.redirect):
                params = urllib.parse.parse_qs(urllib.parse.urlparse(loc).query)
                if params.get('state') == [st]:
                    code = params.get('code', [None])[0]
                s = 0
                break
            s, page, h = c.call('GET', loc)
        if code or s == 0:
            break
        m = re.search(r'action="([^"]+)"', page or '')
        if not m or 'password-new' in page:
            break   # a required action (e.g. password change) means: not ready for takeover
        s, page, h = c.call('POST', m.group(1).replace('&amp;', '&'),
                            {'username': admin['work_phone'][3:], 'password': password, 'credentialId': ''}, form=True)
    del password
    if not code:
        report['login'] = {'result': '目标 Realm 登录未完成（密码错误、账号停用或仍有待办动作）'}
        report['ADMIN_LOGIN_VERIFIED'] = 'FAIL'
        return
    s, tok, _ = c.call('POST', f'/realms/{realm}/protocol/openid-connect/token',
                       {'grant_type': 'authorization_code', 'code': code, 'redirect_uri': args.redirect,
                        'client_id': approved['client'], 'client_secret': private(args.web_secret_file),
                        'code_verifier': verifier}, form=True)
    claims, why = (verify_id_token(c, realm, approved['client'], tok.get('id_token', '..'), nonce) if s == 200 else (None, 'token exchange failed'))
    roles = sorted(((claims or {}).get('resource_access') or {}).get(approved['client'], {}).get('roles', []))
    ok = claims is not None and claims.get('sub') == link['external_subject'] and roles == ['baobei_admin']
    report['login'] = {'result': '已通过目标 Realm 真实登录' if ok else '登录令牌与身份链接或角色不一致：' + (why or str(roles)),
                       'subMatchesLink': bool(claims) and claims.get('sub') == link['external_subject'], 'roles': roles}
    report['ADMIN_LOGIN_VERIFIED'] = 'PASS' if ok else 'FAIL'


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('mode', choices=('admin', 'login'))
    for name in ('--base', '--realm', '--admin', '--link', '--service-secret-file'):
        ap.add_argument(name, required=True)
    ap.add_argument('--password-file')
    ap.add_argument('--web-secret-file')
    ap.add_argument('--redirect')
    args = ap.parse_args()
    report = {'ADMIN_ACCOUNT_VERIFIED': 'NOT_RUN', 'ADMIN_LOGIN_VERIFIED': 'NOT_RUN'}
    try:
        c, admin, link = check_admin(args, report)
        if args.mode == 'login':
            if not (args.password_file and args.web_secret_file and args.redirect):
                raise Refused('login 需要 --password-file、--web-secret-file 与 --redirect')
            if report['ADMIN_ACCOUNT_VERIFIED'] == 'PASS':
                check_login(args, report, c, admin, link)
    except Refused as e:
        report['refused'] = str(e)
    text = json.dumps(report, ensure_ascii=False, indent=2)
    phone = ''
    try:
        phone = json.loads(private(args.admin)).get('work_phone', '')
    except Exception:
        pass
    if phone and (phone in text or phone[3:] in text):
        text = json.dumps({'refused': '输出包含未遮蔽的手机号，已拒绝打印',
                           'ADMIN_ACCOUNT_VERIFIED': 'FAIL', 'ADMIN_LOGIN_VERIFIED': 'NOT_RUN'}, ensure_ascii=False)
    print(text)
    ok = report['ADMIN_ACCOUNT_VERIFIED'] == 'PASS' and (args.mode == 'admin' or report['ADMIN_LOGIN_VERIFIED'] == 'PASS')
    sys.exit(0 if ok else 1)


if __name__ == '__main__':
    main()
