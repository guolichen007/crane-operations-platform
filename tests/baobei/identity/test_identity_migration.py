"""G4-S5-R1-F: identity-migration.py counter-proofs (offline, synthetic data only).

Each test builds a clean synthetic export, proves `verify` passes on it, then
changes exactly one field and proves that verify, links and import all refuse.
"""
import copy
import json
import os
import subprocess
import sys
import tempfile
import unittest
import importlib.util

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, '..', '..', '..', 'deploy', 'baobei', 'identity-migration.py')
spec = importlib.util.spec_from_file_location('idm', TOOL)
idm = importlib.util.module_from_spec(spec)
spec.loader.exec_module(idm)

ISSUER = 'https://crane.youdaozd.com/realms/baobei'


def uid(n):
    return '%08x-0000-4000-8000-%012x' % (0xa0000000 + n, n)


def sid(n):
    return '%08x-1111-4000-8000-%012x' % (0x50000000 + n, n)


def phone(n):
    return '+86139%08d' % (10000000 + n * 7919)


def business():
    rows = [
        # n, role, status, phone?, refs
        (1, 'baobei_admin', 'ACTIVE', True, 0),
        (2, 'baobei_director', 'ACTIVE', True, 3),
        (3, 'baobei_sales', 'ACTIVE', True, 5),
        (4, 'baobei_sales', 'SUSPENDED', True, 1),
        (5, 'baobei_sales', 'ARCHIVED', True, 2),
        (6, 'baobei_sales', 'ACTIVE', False, 0),  # no phone: blocked, never guessed
    ]
    users = []
    for n, role, status, has_phone, refs in rows:
        users.append({'staff_id': sid(n), 'subject': uid(n), 'display_name': '合成%d' % n, 'role': role,
                      'staff_status': status, 'work_phone': phone(n) if has_phone else '', 'projects': refs})
    users.append({'staff_id': sid(7), 'subject': 'pending:' + sid(7), 'display_name': '待开通', 'role': 'baobei_sales',
                  'staff_status': 'PENDING_PROVISION', 'work_phone': phone(7)})
    users.append({'staff_id': sid(8), 'subject': 'synthetic-a', 'display_name': '示例销售 A', 'role': 'baobei_sales',
                  'staff_status': 'ACTIVE', 'work_phone': phone(8)})
    return users


def shared_realm(users):
    out = []
    for u in users:
        if idm.UUID.match(u['subject']):
            # Old usernames are phone-like on purpose: the roster must mask them too.
            out.append({'id': u['subject'], 'username': (u['work_phone'] or '+8613700000000')[3:], 'enabled': True,
                        'baobeiRoles': [u['role']]})
    out.append({'id': uid(99), 'username': '13612345678', 'enabled': True, 'baobeiRoles': ['baobei_sales']})
    return out


def realm_after_import(plan, users):
    """What a correct partialImport produces (exported back)."""
    approved, _ = idm.approved_rows(plan, users)
    out = []
    for p, b in approved:
        e = idm.expected_user(p, b)
        out.append({'id': e['id'], 'username': e['username'], 'enabled': False, 'attributes': copy.deepcopy(e['attributes']),
                    'baobeiRoles': list(e['roles']), 'otherRoles': [], 'groups': [], 'credentials': []})
    out.append({'id': uid(500), 'serviceAccount': True})
    return out


class Fixture:
    def __init__(self):
        self.dir = tempfile.mkdtemp()
        self.users = business()
        self.app_users = self.write('app_users.json', self.users, private=True)
        code, out, err = self.run('roster', '--app-users', self.app_users, '--keycloak-users', self.write('kc.json', shared_realm(self.users)))
        assert code == 0, err
        self.roster_out = out
        self.plan = json.loads(out)
        for p in self.plan['plan']:
            p['approved'] = p['action'] in idm.IMPORT_ACTIONS
        self.realm = realm_after_import(self.plan, self.users)

    def write(self, name, obj, private=False):
        path = os.path.join(self.dir, name)
        if os.path.exists(path):
            os.remove(path)
        with open(path, 'w', encoding='utf-8') as fh:
            json.dump(obj, fh, ensure_ascii=False)
        os.chmod(path, 0o600 if private else 0o644)
        return path

    def run(self, *args):
        r = subprocess.run([sys.executable, '-B', TOOL, *args], capture_output=True, text=True)
        return r.returncode, r.stdout, r.stderr

    def verify(self, realm=None, plan=None, users=None):
        a = self.write('a.json', users if users is not None else self.users, private=True)
        return self.run('verify', '--plan', self.write('plan.json', plan or self.plan), '--app-users', a,
                        '--realm-users', self.write('realm.json', realm if realm is not None else self.realm))

    def links(self, realm=None, plan=None, users=None):
        """Returns (code, stdout, stderr, sql) — the SQL only exists in the private --out file."""
        a = self.write('a.json', users if users is not None else self.users, private=True)
        out = os.path.join(self.dir, 'links-%d.sql' % len(os.listdir(self.dir)))
        code, so, se = self.run('links', '--plan', self.write('plan.json', plan or self.plan), '--app-users', a,
                                '--realm-users', self.write('realm.json', realm if realm is not None else self.realm),
                                '--issuer', ISSUER, '--out', out)
        sql = ''
        if os.path.exists(out):
            if os.name == 'posix':
                assert (os.stat(out).st_mode & 0o077) == 0, 'link SQL file is not private'
            with open(out, encoding='utf-8') as fh:
                sql = fh.read()
        return code, so, se, sql

    def build(self, realm, plan=None, users=None):
        out = os.path.join(self.dir, 'import-%d.json' % len(os.listdir(self.dir)))
        a = self.write('a.json', users if users is not None else self.users, private=True)
        code, so, se = self.run('import', '--plan', self.write('plan.json', plan or self.plan), '--app-users', a,
                                '--realm-users', self.write('cur.json', realm), '--out', out)
        return code, so, se, out

    def all_phones(self):
        return [u['work_phone'] for u in self.users if u['work_phone']] + ['+8613612345678']


class IdentityMigrationTest(unittest.TestCase):
    def setUp(self):
        self.f = Fixture()

    def by_staff(self, realm, n):
        return next(u for u in realm if not u.get('serviceAccount') and u['attributes']['baobei_staff_id'] == [sid(n)])

    def assertRefused(self, mutate_realm=None, mutate_plan=None, mutate_users=None, problem=None):
        realm, plan, users = copy.deepcopy(self.f.realm), copy.deepcopy(self.f.plan), copy.deepcopy(self.f.users)
        if mutate_realm:
            mutate_realm(realm)
        if mutate_plan:
            mutate_plan(plan)
        if mutate_users:
            mutate_users(users)
        code, out, err = self.f.verify(realm, plan, users)
        self.assertNotEqual(code, 0, 'verify accepted a tampered field')
        if problem:
            self.assertIn(problem, out + err)
        code, out, err, sql = self.f.links(realm, plan, users)
        self.assertNotEqual(code, 0, 'links accepted a tampered field')
        self.assertNotIn('INSERT INTO identity_links', out + sql, 'a failed check produced link SQL')
        for p in self.f.all_phones():
            self.assertNotIn(p, out + err)
            self.assertNotIn(p[3:], out + err)

    # --- baseline -----------------------------------------------------------------
    def test_clean_export_passes_and_links_are_guarded(self):
        code, out, err = self.f.verify()
        self.assertEqual(code, 0, out + err)
        self.assertEqual(json.loads(out)['checked'], 5)  # admin, director, sales, suspended, archived with history
        code, out, err, sql = self.f.links()
        self.assertEqual(code, 0, err)
        self.assertEqual(sql.count('INSERT INTO identity_links'), 5)
        self.assertTrue(sql.startswith('BEGIN;') and sql.rstrip().endswith('COMMIT;'))
        self.assertIn('roster is stale', sql)
        # G5-RC0: the live re-check covers status and work phone, and the terminal
        # never shows the phones the SQL carries.
        self.assertIn("staff_status='ACTIVE'", sql)
        self.assertIn("work_phone='%s'" % phone(1), sql)
        for p in self.f.all_phones():
            self.assertNotIn(p, out + err)
            self.assertNotIn(p[3:], out + err)

    def test_expected_states_per_category(self):
        acts = {p['staffId']: p['action'] for p in self.f.plan['plan']}
        self.assertEqual(acts[sid(1)], 'IMPORT')
        self.assertEqual(acts[sid(4)], 'IMPORT_DISABLED')               # suspended → disabled, role kept
        self.assertEqual(acts[sid(5)], 'IMPORT_DISABLED_KEEP_HISTORY')  # archived with history → disabled, no role
        self.assertEqual(acts[sid(6)], 'IMPORT_BLOCKED')                # missing phone → never guessed
        self.assertEqual(acts[sid(7)], 'CREATE_IN_NEW_REALM_AFTER_SWITCH')
        self.assertEqual(acts[sid(8)], 'EXCLUDE')
        archived = self.by_staff(self.f.realm, 5)
        self.assertEqual(archived['baobeiRoles'], [])
        # Suspended / archived may never be enabled, even after the enable step.
        for n in (4, 5):
            realm = copy.deepcopy(self.f.realm)
            self.by_staff(realm, n)['enabled'] = True
            a = self.f.write('a.json', self.f.users, private=True)
            code, out, _ = self.f.run('verify', '--plan', self.f.write('plan.json', self.f.plan), '--app-users', a,
                                      '--realm-users', self.f.write('realm.json', realm), '--after-enable')
            self.assertNotEqual(code, 0, n)
            self.assertIn('意外启用', out)
        # An active member may be enabled after the controlled enable step.
        realm = copy.deepcopy(self.f.realm)
        self.by_staff(realm, 3)['enabled'] = True
        a = self.f.write('a.json', self.f.users, private=True)
        code, out, _ = self.f.run('verify', '--plan', self.f.write('plan.json', self.f.plan), '--app-users', a,
                                  '--realm-users', self.f.write('realm.json', realm), '--after-enable')
        self.assertEqual(code, 0, out)
        # Not bound / synthetic / blocked rows must not exist in the new realm.
        for n in (6, 7, 8):
            realm = copy.deepcopy(self.f.realm)
            realm.append({'id': uid(600 + n), 'username': self.f.users[n - 1]['work_phone'][3:] or '13800000000', 'enabled': False,
                          'attributes': {'baobei_staff_id': [sid(n)]}, 'baobeiRoles': ['baobei_sales'], 'credentials': []})
            code, out, err = self.f.verify(realm)
            self.assertNotEqual(code, 0, n)

    # --- required counter-proofs -------------------------------------------------
    def test_wrong_phone(self):
        self.assertRefused(lambda r: self.by_staff(r, 3).update(username='13900000001'), problem='登录名与批准的手机号不一致')

    def test_plus86_login_is_not_the_same_login(self):
        self.assertRefused(lambda r: self.by_staff(r, 3).update(username='+86' + self.by_staff(r, 3)['username']))

    def test_wrong_staff_id(self):
        self.assertRefused(lambda r: self.by_staff(r, 3)['attributes'].update(baobei_staff_id=[sid(42)]), problem='员工编号属性不一致')

    def test_wrong_staff_id_in_plan(self):
        def swap(plan):
            a = next(p for p in plan['plan'] if p['staffId'] == sid(2))
            b = next(p for p in plan['plan'] if p['staffId'] == sid(3))
            a['staffId'], b['staffId'] = b['staffId'], a['staffId']
        self.assertRefused(mutate_plan=swap)

    def test_wrong_subject(self):
        self.assertRefused(lambda r: self.by_staff(r, 3)['attributes'].update(baobei_subject=[uid(2)]))

    def test_role_mismatch(self):
        self.assertRefused(lambda r: self.by_staff(r, 3).update(baobeiRoles=['baobei_admin']), problem='报备角色不一致')

    def test_extra_role(self):
        self.assertRefused(lambda r: self.by_staff(r, 3).update(baobeiRoles=['baobei_sales', 'baobei_manager']))

    def test_crane_role_or_group(self):
        self.assertRefused(lambda r: self.by_staff(r, 3).update(otherRoles=['crane_admin']), problem='带有报备以外的角色或组')
        self.assertRefused(lambda r: self.by_staff(r, 3).update(groups=['/crane']))

    def test_role_tampered_in_plan(self):
        self.assertRefused(mutate_plan=lambda p: next(x for x in p['plan'] if x['staffId'] == sid(3)).update(role='baobei_admin'))

    def test_duplicate_uuid(self):
        def dup(r):
            twin = copy.deepcopy(self.by_staff(r, 3))
            r.append(twin)
        self.assertRefused(dup, problem='UUID 重复')

    def test_two_accounts_map_to_one_member(self):
        def dup(r):
            twin = copy.deepcopy(self.by_staff(r, 3))
            twin['id'], twin['username'] = uid(700), '13911112222'
            r.append(twin)
        self.assertRefused(dup, problem='多个账号映射到同一')

    def test_unexpected_enabled(self):
        self.assertRefused(lambda r: self.by_staff(r, 3).update(enabled=True), problem='意外启用')

    def test_extra_account(self):
        self.assertRefused(lambda r: r.append({'id': uid(800), 'username': 'stranger', 'enabled': False, 'attributes': {},
                                               'baobeiRoles': [], 'credentials': []}), problem='计划外的额外账号')

    def test_credentials_present(self):
        self.assertRefused(lambda r: self.by_staff(r, 3).update(credentials=[{'type': 'password'}]), problem='存在凭据')

    def test_missing_account(self):
        self.assertRefused(lambda r: r.remove(self.by_staff(r, 3)), problem='缺失账号')

    def test_member_missing_from_plan(self):
        self.assertRefused(mutate_plan=lambda p: p['plan'].pop())

    def test_unapproved_action_cannot_be_approved(self):
        self.assertRefused(mutate_plan=lambda p: next(x for x in p['plan'] if x['staffId'] == sid(6)).update(approved=True))
        self.assertRefused(mutate_plan=lambda p: next(x for x in p['plan'] if x['staffId'] == sid(7)).update(approved=True, action='IMPORT'))

    def test_new_uuid_tampered_in_plan(self):
        self.assertRefused(mutate_plan=lambda p: next(x for x in p['plan'] if x['staffId'] == sid(3)).update(newUuid=uid(3)))

    # --- import: conflicts are refused, SKIP is never evidence -------------------
    def test_import_refuses_same_uuid_with_other_login_or_owner(self):
        code, out, err, path = self.f.build([])
        self.assertEqual(code, 0, out + err)
        with open(path, encoding='utf-8') as fh:
            imp = json.load(fh)
        self.assertEqual(imp['ifResourceExists'], 'FAIL')
        self.assertEqual(len(imp['users']), 5)
        self.assertEqual(oct(os.stat(path).st_mode & 0o777), '0o600')
        for u in imp['users']:
            self.assertFalse(u['enabled'])
            for k in ('credentials', 'email', 'groups', 'realmRoles'):
                self.assertNotIn(k, u)
        # Same UUID but a different login → refused, nothing written.
        realm = copy.deepcopy(self.f.realm)
        self.by_staff(realm, 3)['username'] = '13900000001'
        code, out, err, path = self.f.build(realm)
        self.assertEqual(code, 2, out + err)
        self.assertIn('REFUSED', out)
        self.assertFalse(os.path.exists(path))
        # Same UUID but owned by another member → refused.
        realm = copy.deepcopy(self.f.realm)
        self.by_staff(realm, 3)['attributes']['baobei_staff_id'] = [sid(2)]
        self.assertEqual(self.f.build(realm)[0], 2)
        # Approved login already used by an unrelated account → refused.
        realm = [{'id': uid(900), 'username': phone(3)[3:], 'enabled': False, 'attributes': {}, 'baobeiRoles': [], 'credentials': []}]
        self.assertEqual(self.f.build(realm)[0], 2)
        # Identical existing users are reported as present, not re-imported.
        code, out, err, path = self.f.build(self.f.realm)
        self.assertEqual(code, 0, out + err)
        self.assertEqual(len(json.loads(out)['alreadyPresentIdentical']), 5)

    def test_unapproved_member_already_in_new_realm(self):
        # A not-yet-bound member (pending:) already has a login in the new realm.
        stray = {'id': uid(610), 'username': phone(7)[3:], 'enabled': False, 'attributes': {'baobei_staff_id': [sid(7)]},
                 'baobeiRoles': ['baobei_sales'], 'credentials': []}
        realm = copy.deepcopy(self.f.realm) + [stray]
        code, out, err = self.f.verify(realm)
        self.assertNotEqual(code, 0)
        self.assertIn('未批准导入的成员在新 Realm 中已有账号', out)
        code, out, err, path = self.f.build([stray])
        self.assertEqual(code, 2, out + err)
        self.assertIn('未批准导入的成员在新 Realm 中已有账号', out)
        self.assertFalse(os.path.exists(path))

    def test_import_never_writes_over_an_existing_file(self):
        code, out, err, path = self.f.build([])
        self.assertEqual(code, 0)
        a = self.f.write('a.json', self.f.users, private=True)
        code, out, err = self.f.run('import', '--plan', self.f.write('plan.json', self.f.plan), '--app-users', a,
                                    '--realm-users', self.f.write('cur.json', []), '--out', path)
        self.assertNotEqual(code, 0)

    # --- redaction ---------------------------------------------------------------
    def test_roster_masks_every_phone_and_phone_like_username(self):
        out = self.f.roster_out
        for p in self.f.all_phones():
            self.assertNotIn(p, out)
            self.assertNotIn(p[3:], out)
        plan = json.loads(out)
        self.assertTrue(all('****' in p['oldUsername'] for p in plan['plan'] if p['oldUsername']))
        self.assertEqual(plan['keycloakOnly'][0]['username'], '136****5678')

    def test_business_export_must_be_private(self):
        path = self.f.write('open.json', self.f.users)
        os.chmod(path, 0o644)
        code, out, err = self.f.run('roster', '--app-users', path, '--keycloak-users', self.f.write('kc.json', []))
        self.assertEqual(code, 2)
        self.assertIn('chmod 600', err)

    def test_output_guard_refuses_raw_phone(self):
        guard = idm.Out(self.f.users)
        with self.assertRaises(idm.Refused):
            guard.emit({'x': phone(3)[3:]})


if __name__ == '__main__':
    unittest.main()
