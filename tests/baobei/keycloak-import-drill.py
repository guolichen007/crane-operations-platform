"""G4-S5-R1-E: UUID-preserving import, then forward switch and switch back, on a real
Keycloak 26.8.1 (loopback, disposable). Run after keycloak-realm-probe.py in the same
Keycloak process (realms `crane` and `baobei` exist).

Synthetic roster: 10 business rows like the historical report — 6 bound staff
(1 suspended), 1 archived with history, 1 not yet bound (pending:), 2 synthetic.
"""
import json, os, secrets, subprocess, sys, tempfile, importlib.util

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('probe', os.path.join(HERE, 'keycloak-realm-probe.py'))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
call, check, admin_token, token, client_uuid, browser_login = probe.call, probe.check, probe.admin_token, probe.token, probe.client_uuid, probe.browser_login
TOOL = os.path.join(HERE, '..', '..', 'deploy', 'baobei', 'identity-migration.py')


def run_tool(*args):
    r = subprocess.run([sys.executable, '-B', TOOL, *args], capture_output=True, text=True)
    return r.returncode, r.stdout, r.stderr


def main():
    adm = admin_token()
    old_pw = secrets.token_urlsafe(18)  # per-run synthetic shared-realm password
    crane_web = client_uuid(adm, 'crane', 'crane-web')
    # Disposable loopback Keycloak only: start the dedicated realm empty (the probe
    # leaves its own users there), so verify can demand "no extra accounts".
    s, left, _ = call('GET', '/admin/realms/baobei/users?max=500', token=adm)
    for u in left or []:
        if not u.get('username', '').startswith('service-account-'):
            call('DELETE', f"/admin/realms/baobei/users/{u['id']}", token=adm)
    # Shared realm: a baobei-web client with the four roles, and the historical users.
    call('POST', '/admin/realms/crane/clients', {'clientId': 'baobei-web', 'publicClient': False, 'secret': secrets.token_urlsafe(24),
         'standardFlowEnabled': True, 'redirectUris': ['http://127.0.0.1/cb']}, adm)
    bw = client_uuid(adm, 'crane', 'baobei-web')
    for r in probe_roles():
        call('POST', f'/admin/realms/crane/clients/{bw}/roles', {'name': r}, adm)
    rows, kc_export, phones = [], [], {}
    people = [('张合成', 'baobei_admin', 'ACTIVE'), ('李合成', 'baobei_director', 'ACTIVE'), ('王合成', 'baobei_manager', 'ACTIVE'),
              ('赵合成', 'baobei_sales', 'ACTIVE'), ('钱合成', 'baobei_sales', 'ACTIVE'), ('孙合成', 'baobei_sales', 'SUSPENDED'),
              ('周合成', 'baobei_sales', 'ARCHIVED')]
    for i, (name, role, status) in enumerate(people):
        s, _, h = call('POST', '/admin/realms/crane/users', {'username': f'legacy-{i}-{secrets.token_hex(3)}', 'enabled': True,
                       'email': f'legacy{i}@example.test', 'credentials': [{'type': 'password', 'value': old_pw, 'temporary': False}]}, adm)
        uid = h['Location'].rsplit('/', 1)[-1]
        s, role_rep, _ = call('GET', f'/admin/realms/crane/clients/{bw}/roles/{role}', token=adm)
        call('POST', f'/admin/realms/crane/users/{uid}/role-mappings/clients/{bw}', [role_rep], adm)
        # One historical user also has a Crane role; it must not travel.
        if i == 0:
            s, cr, _ = call('GET', f'/admin/realms/crane/clients/{crane_web}/roles/crane_admin', token=adm)
            call('POST', f'/admin/realms/crane/users/{uid}/role-mappings/clients/{crane_web}', [cr], adm)
        staff_id = f'5{i}5{i}5{i}5{i}-0000-4000-8000-00000000000{i}'
        phone = '' if i == 4 else f'+86139{secrets.randbelow(10**8):08d}'
        rows.append({'staff_id': staff_id, 'subject': uid, 'display_name': name, 'role': role, 'staff_status': status,
                     'work_phone': phone, 'projects': 1 if i in (3, 6) else 0, 'approvals': 1 if i == 2 else 0})
        if phone:
            phones[staff_id] = phone
        kc_export.append({'id': uid, 'username': f'legacy-{i}', 'enabled': True, 'baobeiRoles': [role]})
    rows.append({'staff_id': '57575757-0000-4000-8000-000000000007', 'subject': 'pending:57575757-0000-4000-8000-000000000007',
                 'display_name': '吴合成', 'role': 'baobei_sales', 'staff_status': 'PENDING_PROVISION', 'work_phone': '+8613912345678'})
    rows.append({'staff_id': '58585858-0000-4000-8000-000000000008', 'subject': 'synthetic-a', 'display_name': '示例销售 A', 'role': 'baobei_sales', 'staff_status': 'ACTIVE'})
    rows.append({'staff_id': '59595959-0000-4000-8000-000000000009', 'subject': 'synthetic-admin', 'display_name': '示例管理员', 'role': 'baobei_admin', 'staff_status': 'ACTIVE'})
    tmp = tempfile.mkdtemp()
    def dump(name, obj):
        p = os.path.join(tmp, name)
        if os.path.exists(p):
            os.remove(p)
        fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w', encoding='utf-8') as fh:
            json.dump(obj, fh, ensure_ascii=False)
        return p

    def realm_export():
        s, users, _ = call('GET', '/admin/realms/baobei/users?max=500&briefRepresentation=false', token=adm)
        out = []
        for u in users:
            if u.get('username', '').startswith('service-account-'):
                out.append({'id': u['id'], 'serviceAccount': True})
                continue
            s, creds, _ = call('GET', f"/admin/realms/baobei/users/{u['id']}/credentials", token=adm)
            s, cr, _ = call('GET', f"/admin/realms/baobei/users/{u['id']}/role-mappings", token=adm)
            cm = cr.get('clientMappings') or {}
            s, groups, _ = call('GET', f"/admin/realms/baobei/users/{u['id']}/groups", token=adm)
            out.append({'id': u['id'], 'username': u['username'], 'enabled': u['enabled'], 'attributes': u.get('attributes') or {},
                        'baobeiRoles': [r['name'] for r in cm.get('baobei-web', {}).get('mappings', [])],
                        'otherRoles': [k for k in cm if k != 'baobei-web'] + [r['name'] for r in cr.get('realmMappings') or [] if r['name'] != 'default-roles-baobei'],
                        'groups': [g['path'] for g in groups or []], 'credentials': [{'type': c.get('type')} for c in creds or []]})
        return out
    app_users = dump('app_users.json', rows)
    code, out, err = run_tool('roster', '--app-users', app_users, '--keycloak-users', dump('kc.json', kc_export))
    check('roster runs on the synthetic export', code == 0, err)
    plan = json.loads(out)
    acts = plan['actions']
    check('roster: 10 rows classified (5 import, 1 blocked: no phone, 1 suspended, 1 archived w/ history, 1 not bound, 2 synthetic... )',
          plan['businessRows'] == 10 and acts.get('EXCLUDE') == 2 and acts.get('CREATE_IN_NEW_REALM_AFTER_SWITCH') == 1
          and acts.get('IMPORT_BLOCKED') == 1 and acts.get('IMPORT_DISABLED') == 1 and acts.get('IMPORT_DISABLED_KEEP_HISTORY') == 1
          and acts.get('IMPORT') == 4, acts)
    check('roster never prints a full phone number', not any(p in out for p in phones.values()))
    for p in plan['plan']:
        p['approved'] = p['action'].startswith('IMPORT') and p['action'] != 'IMPORT_BLOCKED'
    plan_path = dump('plan.json', plan)
    pre = [u for u in realm_export() if not u.get('serviceAccount')]
    imp_path = os.path.join(tmp, 'import.json')
    code, out, err = run_tool('import', '--plan', plan_path, '--app-users', app_users, '--realm-users', dump('pre.json', pre), '--out', imp_path)
    check('import file built after checking the current realm', code == 0, out + err)
    with open(imp_path, encoding='utf-8') as fh:
        imp = json.load(fh)
    check('import file is private (0600)', (os.stat(imp_path).st_mode & 0o777) == 0o600)
    check('import file: 6 users, FAIL on conflicts, disabled, no credentials/groups/realm roles/email; archived has no role',
          imp['ifResourceExists'] == 'FAIL' and len(imp['users']) == 6 and all(
              not u['enabled'] and 'credentials' not in u and 'groups' not in u and 'realmRoles' not in u and 'email' not in u
              for u in imp['users']) and sum(1 for u in imp['users'] if 'clientRoles' not in u) == 1, imp)
    # partialImport into the dedicated realm.
    s, res, _ = call('POST', '/admin/realms/baobei/partialImport', imp, adm)
    check('partialImport accepted', s == 200, res)
    ids = {u['id'] for u in imp['users']}
    s, users, _ = call('GET', '/admin/realms/baobei/users?max=200&briefRepresentation=false', token=adm)
    imported = {u['id']: u for u in users if u['id'] in ids}
    check('new-realm users created with the planned deterministic ids', set(imported) == ids, sorted(ids - set(imported)))
    approved = {p['expectedUuid'] for p in plan['plan'] if p['approved']}
    check('every user links back to its unchanged business subject (baobei_subject)',
          {(u.get('attributes') or {}).get('baobei_subject', [''])[0] for u in imported.values()} == approved,
          [u.get('attributes') for u in imported.values()])
    s, cu, _ = call('GET', '/admin/realms/crane/users?max=200', token=adm)
    check('shared-realm accounts untouched (switch back possible)', approved <= {u['id'] for u in cu})
    check('imported users disabled', all(not u['enabled'] for u in imported.values()))
    nocred = True
    roles_ok = True
    export = []
    web = client_uuid(adm, 'baobei', 'baobei-web')
    rm = client_uuid(adm, 'baobei', 'realm-management')
    for uid, u in imported.items():
        s, creds, _ = call('GET', f'/admin/realms/baobei/users/{uid}/credentials', token=adm)
        nocred &= creds == []
        s, cr, _ = call('GET', f'/admin/realms/baobei/users/{uid}/role-mappings', token=adm)
        client_maps = cr.get('clientMappings') or {}
        b_roles = [r['name'] for r in client_maps.get('baobei-web', {}).get('mappings', [])]
        other = [k for k in client_maps if k != 'baobei-web']
        realm_roles = [r['name'] for r in cr.get('realmMappings') or [] if r['name'] != 'default-roles-baobei']
        roles_ok &= len(b_roles) <= 1 and not other and not realm_roles
        export.append({'id': uid, 'username': u['username'], 'enabled': u['enabled'], 'baobeiRoles': b_roles,
                       'otherRoles': other + realm_roles, 'credentials': creds, 'attributes': u.get('attributes')})
    check('no password credentials carried over', nocred)
    check('at most one baobei-web role each (archived none); no Crane or realm roles', roles_ok)
    realm_now = realm_export()
    code, out, err = run_tool('verify', '--plan', plan_path, '--app-users', app_users, '--realm-users', dump('realm.json', realm_now))
    check('verify: realm matches the approved plan field by field', code == 0, out + err)
    check('verify output masks every phone', not any(p in out + err or p[3:] in out + err for p in phones.values()))
    # Tamper on the real realm: change one approved user's login, verify and links must refuse.
    victim = next(u for u in imp['users'] if 'clientRoles' in u)
    call('PUT', f"/admin/realms/baobei/users/{victim['id']}", {'username': '13000000009'}, adm)
    code, out, err = run_tool('verify', '--plan', plan_path, '--app-users', app_users, '--realm-users', dump('realm.json', realm_export()))
    check('verify refuses a real user whose login differs from the approved phone', code != 0 and '登录名与批准的手机号不一致' in out, out + err)
    code, out, err = run_tool('links', '--plan', plan_path, '--app-users', app_users, '--realm-users', dump('realm.json', realm_export()),
                              '--issuer', 'https://crane.youdaozd.com/realms/baobei')
    check('links refuses and prints no link SQL after a failed check', code != 0 and 'INSERT INTO identity_links' not in out, out + err)
    code, out, err = run_tool('import', '--plan', plan_path, '--app-users', app_users, '--realm-users', dump('cur.json', realm_export()),
                              '--out', os.path.join(tmp, 'import-again.json'))
    check('import refuses: same UUID with a different login (never SKIP)', code == 2 and not os.path.exists(os.path.join(tmp, 'import-again.json')), out + err)
    call('PUT', f"/admin/realms/baobei/users/{victim['id']}", {'username': victim['username']}, adm)
    # A conflicting re-import (changed login for an existing id) is refused; the user is never overwritten.
    clash = json.loads(json.dumps(imp))
    clash['users'][0]['username'] = '13000000000'
    s, res, _ = call('POST', '/admin/realms/baobei/partialImport', clash, adm)
    s2, u0, _ = call('GET', f"/admin/realms/baobei/users/{clash['users'][0]['id']}", token=adm)
    # ifResourceExists=FAIL: Keycloak answers 409; either way
    # (refused or skipped) the existing user must be unchanged — never overwritten.
    check('conflicting re-import refused, existing user never overwritten', s == 409 and u0['username'] == imp['users'][0]['username'], (s, res, u0.get('username')))
    s, res, _ = call('POST', '/admin/realms/baobei/partialImport', imp, adm)
    check('identical re-import with FAIL is refused by Keycloak (no silent acceptance)', s == 409, (s, res))
    code, out, err = run_tool('import', '--plan', plan_path, '--app-users', app_users, '--realm-users', dump('cur.json', realm_export()),
                              '--out', os.path.join(tmp, 'import-identical.json'))
    check('identical users are reported as present; nothing left to import', code == 0 and len(json.loads(out)['alreadyPresentIdentical']) == 6, out + err)
    # Old credentials do not work in the new realm.
    first = next(u for u in imp['users'] if u.get('clientRoles', {}).get('baobei-web') == ['baobei_sales'])
    loc, _ = browser_login('baobei', 'baobei-web', 'http://127.0.0.1/cb', first['username'], old_pw)
    check('old shared-realm password does not sign in to the new realm', 'code=' not in loc, loc)
    code, out, err = run_tool('links', '--plan', plan_path, '--app-users', app_users, '--realm-users', dump('realm.json', realm_export()),
                              '--issuer', 'https://crane.youdaozd.com/realms/baobei')
    check('links: verified first, one guarded insert per imported user in one transaction',
          code == 0 and out.count('INSERT INTO identity_links') == 6 and out.startswith('BEGIN;'), out + err)
    with open(os.path.expanduser('~/kc/import.json'), 'w') as fh:
        json.dump({'first': first, 'tmp': tmp}, fh)
    failed = [n for n, ok in probe.results if not ok]
    print(json.dumps({'passed': sum(1 for _, ok in probe.results if ok), 'failed': failed}, ensure_ascii=False))
    sys.exit(1 if failed else 0)


def probe_roles():
    return ['baobei_sales', 'baobei_manager', 'baobei_director', 'baobei_admin']


if __name__ == '__main__':
    main()
