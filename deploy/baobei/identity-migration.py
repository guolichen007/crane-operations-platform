#!/usr/bin/env python3
"""Baobei identity migration tools (server CC runs these; no production writes).

G4-S5-R1-E introduced the tool; G4-S5-R1-F made every step verify identities
field by field and refuse on any doubt.

Inputs (read-only exports; see docs/项目报备中心/42 号):
  --app-users     Baobei app_users export (staff_id, subject, display_name, role,
                  staff_status, work_phone, reference counts). It holds the raw work
                  phones, so it must be a private file (mode 0600, outside Git); the
                  tool refuses a group/other-readable file.
  --keycloak-users  shared-realm users with their baobei-web roles (roster only).
  --realm-users   dedicated-realm users: id, username, enabled, attributes,
                  baobeiRoles, otherRoles, groups, credentials (types only),
                  serviceAccount.

Commands:
  roster  READ-ONLY. Classifies every business row and prints a plan in which
          every phone and every phone-like username is masked.
  import  From the APPROVED plan, writes the partialImport file to a new private
          file (--out, 0600): approved rows only, disabled, no credentials, exact
          single role (archived: none), ifResourceExists=FAIL. Before that it
          compares the plan with the business export and the current realm and
          refuses on any conflict (same UUID, login or staff id with different
          content). An identical existing user is reported, never silently taken.
  verify  Field-by-field check of the realm against plan + business export:
          new UUID, staff_id, business subject, exact login (11 digits of the
          approved phone), role, enabled state, no credentials; no extra users,
          no duplicates; rows that must not exist (not bound, synthetic, blocked,
          unapproved) are absent. Exit 1 on any failure.
  links   Runs verify first; on success writes (--out, new 0600 file) one SQL
          transaction that re-checks staff id, subject, role, status and work
          phone of every row in the live database (a stale roster aborts it) and
          inserts identity_links. On failure writes nothing and exits 1.

Keycloak user ids are unique per server, so a new-realm user cannot reuse the
shared-realm UUID while that account exists. New users get UUIDv5(subject) and
carry baobei_subject; the business subject and every history row never change.
No secret is read or written. Terminal output never contains a full phone.
"""
import argparse
import json
import os
import re
import stat
import sys
import uuid as uuidlib

# Namespace for deterministic new-realm user ids (UUIDv5 of the business subject).
LINK_NS = uuidlib.UUID('7b3c0b1e-3f4e-4b5a-9a7e-6c2d1f0e9b81')

UUID = re.compile(r'^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')
MAINLAND = re.compile(r'^\+861[3-9]\d{9}$')
SYNTHETIC = re.compile(r'^(synthetic-|preview-|g[0-9]|fixture)', re.I)
ROLES = {'baobei_sales', 'baobei_manager', 'baobei_director', 'baobei_admin'}
REF_KEYS = ('projects', 'created', 'approvals', 'participations', 'audit', 'activities')
# Plan action → business status it requires and the expected realm state.
IMPORT_ACTIONS = {
    'IMPORT': {'status': {'ACTIVE', 'PENDING_ACTIVATION'}, 'role': True},
    'IMPORT_DISABLED': {'status': {'SUSPENDED'}, 'role': True},
    'IMPORT_DISABLED_KEEP_HISTORY': {'status': {'ARCHIVED'}, 'role': False},
}


class Refused(Exception):
    pass


def new_realm_id(subject):
    return str(uuidlib.uuid5(LINK_NS, 'baobei:' + subject))


def mask(phone):
    return (phone[:-8] + '****' + phone[-4:]) if phone and len(phone) >= 8 else (phone or '')


DIGITS = re.compile(r'\d{7,}')


def mask_name(name):
    """Usernames may be phone numbers: mask every run of 7+ digits."""
    return DIGITS.sub(lambda m: m.group(0)[:3] + '****' + m.group(0)[-4:], name or '')


def load(path, private=False):
    if private and os.name == 'posix':
        mode = os.stat(path).st_mode
        if mode & (stat.S_IRWXG | stat.S_IRWXO):
            raise Refused('%s 含原始手机号，必须是仅本人可读的私有文件（chmod 600）' % os.path.basename(path))
    with open(path, encoding='utf-8') as fh:
        return json.load(fh)


def write_private(path, text):
    """New file only (never overwrite), mode 0600."""
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w', encoding='utf-8') as fh:
        fh.write(text)


class Out:
    """Terminal output guard: refuses to print any raw phone or 11-digit login."""

    def __init__(self, users):
        self.secrets = set()
        for u in users:
            p = u.get('work_phone') or ''
            if p:
                self.secrets.add(p)
                if MAINLAND.match(p):
                    self.secrets.add(p[3:])

    def emit(self, obj):
        text = json.dumps(obj, ensure_ascii=False, indent=2)
        if any(s in text for s in self.secrets):
            raise Refused('输出包含未遮蔽的手机号，已拒绝打印')
        print(text)


def by_staff(users):
    out = {}
    for u in users:
        if u['staff_id'] in out:
            raise Refused('业务导出中 staff_id 重复：%s' % u['staff_id'])
        out[u['staff_id']] = u
    subjects = [u['subject'] for u in users]
    if len(subjects) != len(set(subjects)):
        raise Refused('业务导出中 subject 重复')
    phones = [u['work_phone'] for u in users if u.get('work_phone')]
    if len(phones) != len(set(phones)):
        raise Refused('业务导出中工作手机重复')
    return out


def classify(row, kc_by_id):
    """One business row → (category, action, notes)."""
    subject = row['subject']
    status = row['staff_status']
    refs = sum(int(row.get(k) or 0) for k in REF_KEYS)
    notes = []
    if SYNTHETIC.match(subject) or SYNTHETIC.match(row.get('display_name') or ''):
        return 'SYNTHETIC', 'EXCLUDE', notes
    if subject.startswith('pending:'):
        return 'NOT_BOUND', 'CREATE_IN_NEW_REALM_AFTER_SWITCH', ['尚未绑定企业账号：切换后由管理员在网页开户']
    if not UUID.match(subject):
        return 'NON_UUID_SUBJECT', 'MANUAL_REVIEW', ['subject 不是 Keycloak UUID']
    kc = kc_by_id.get(subject)
    if kc is None:
        return 'NO_KEYCLOAK_USER', 'MANUAL_REVIEW', ['旧 Realm 中找不到该 UUID'] + (['有历史引用，不得删除业务记录'] if refs else [])
    if status == 'ARCHIVED':
        return 'ARCHIVED', 'IMPORT_DISABLED_KEEP_HISTORY' if refs else 'SKIP', notes
    phone = row.get('work_phone') or ''
    if not phone:
        notes.append('缺少工作手机：不得猜测，需管理员补全后才能设置登录手机号')
    elif not MAINLAND.match(phone):
        notes.append('非中国大陆手机号：当前登录只支持 11 位大陆手机')
    roles = set(kc.get('baobeiRoles') or [])
    if roles != {row['role']}:
        notes.append('旧 Realm 角色与业务角色不一致：%s' % sorted(roles))
    ok = bool(phone) and bool(MAINLAND.match(phone)) and roles == {row['role']}
    if status == 'SUSPENDED':
        return 'SUSPENDED', 'IMPORT_DISABLED' if ok else 'IMPORT_BLOCKED', notes
    category = 'ADMIN' if row['role'] == 'baobei_admin' else 'STAFF'
    return category, 'IMPORT' if ok and status in IMPORT_ACTIONS['IMPORT']['status'] else 'IMPORT_BLOCKED', notes


def roster(args):
    users = load(args.app_users, private=True)
    out = Out(users)
    by_staff(users)
    kc_users = load(args.keycloak_users)
    kc_by_id = {u['id']: u for u in kc_users}
    plan = []
    for row in sorted(users, key=lambda r: (r['role'], r['display_name'], r['staff_id'])):
        category, action, notes = classify(row, kc_by_id)
        kc = kc_by_id.get(row['subject'], {})
        plan.append({
            'staffId': row['staff_id'], 'expectedUuid': row['subject'],
            'newUuid': new_realm_id(row['subject']) if action in IMPORT_ACTIONS else '',
            'displayName': row['display_name'], 'role': row['role'], 'status': row['staff_status'],
            'category': category, 'action': action, 'approved': False,
            'phone': mask(row.get('work_phone')), 'phoneReady': bool(row.get('work_phone') and MAINLAND.match(row['work_phone'])),
            'oldUsername': mask_name(kc.get('username', '')), 'oldEnabled': kc.get('enabled'),
            'references': {k: int(row.get(k) or 0) for k in REF_KEYS},
            'notes': notes,
        })
    counts = {}
    for p in plan:
        counts[p['action']] = counts.get(p['action'], 0) + 1
    subjects = {r['subject'] for r in users}
    unmatched = [u for u in kc_users if u['id'] not in subjects and u.get('baobeiRoles')]
    out.emit({'businessRows': len(users), 'keycloakUsersWithBaobeiRole': sum(1 for u in kc_users if u.get('baobeiRoles')),
              'actions': counts,
              'keycloakOnly': [{'uuid': u['id'], 'username': mask_name(u.get('username', '')), 'roles': u['baobeiRoles']} for u in unmatched],
              'plan': plan})


def approved_rows(plan, users):
    """Cross-check the (possibly hand-edited) plan against the business export.

    Returns (approved, absent): approved = [(plan row, business row)]; absent =
    business rows whose identity must NOT exist in the dedicated realm.
    """
    staff = by_staff(users)
    seen_staff, seen_uuid = set(), set()
    approved, absent = [], []
    rows = plan.get('plan')
    if not isinstance(rows, list):
        raise Refused('计划格式无效')
    for p in rows:
        sid = p.get('staffId')
        b = staff.get(sid)
        if b is None:
            raise Refused('计划中的员工编号不在业务导出中：%s' % sid)
        if sid in seen_staff:
            raise Refused('计划中员工编号重复：%s' % sid)
        seen_staff.add(sid)
        if p.get('expectedUuid') != b['subject']:
            raise Refused('计划中的业务 subject 与员工编号不对应：%s' % sid)
        if p.get('role') != b['role'] or p.get('status') != b['staff_status']:
            raise Refused('计划中的角色或状态与业务记录不一致：%s' % sid)
        action = p.get('action')
        if not p.get('approved'):
            absent.append(b)
            continue
        if action not in IMPORT_ACTIONS:
            raise Refused('计划行 %s 的动作 %s 不能批准导入' % (sid, action))
        rule = IMPORT_ACTIONS[action]
        if b['staff_status'] not in rule['status']:
            raise Refused('计划行 %s 的动作与业务状态不符' % sid)
        if not UUID.match(b['subject']) or b['role'] not in ROLES:
            raise Refused('计划行 %s 的 subject 或角色无效' % sid)
        phone = b.get('work_phone') or ''
        if not MAINLAND.match(phone):
            raise Refused('批准行 %s 没有可用的大陆手机号（不得猜测）' % sid)
        nid = new_realm_id(b['subject'])
        if p.get('newUuid') not in ('', None, nid):
            raise Refused('计划行 %s 的新 UUID 被修改' % sid)
        if nid in seen_uuid:
            raise Refused('重复的新 UUID')
        seen_uuid.add(nid)
        approved.append((p, b))
    missing = set(staff) - seen_staff
    if missing:
        raise Refused('计划缺少业务导出中的成员：%d 行' % len(missing))
    return approved, absent


def expected_user(p, b):
    rule = IMPORT_ACTIONS[p['action']]
    return {
        'id': new_realm_id(b['subject']), 'username': b['work_phone'][3:],
        'attributes': {'baobei_staff_id': [b['staff_id']], 'baobei_subject': [b['subject']]},
        'roles': [b['role']] if rule['role'] else [],
    }


def compare(exp, u, after_enable, action):
    """Field-by-field differences between the expected and the actual realm user."""
    bad = []
    if u.get('username') != exp['username']:
        bad.append('登录名与批准的手机号不一致')
    attrs = u.get('attributes') or {}
    if attrs.get('baobei_staff_id') != exp['attributes']['baobei_staff_id']:
        bad.append('员工编号属性不一致')
    if attrs.get('baobei_subject') != exp['attributes']['baobei_subject']:
        bad.append('业务 subject 属性不一致')
    if sorted(u.get('baobeiRoles') or []) != exp['roles']:
        bad.append('报备角色不一致')
    if u.get('otherRoles') or u.get('groups'):
        bad.append('带有报备以外的角色或组')
    if u.get('credentials'):
        bad.append('存在凭据')
    enabled = bool(u.get('enabled'))
    may_enable = after_enable and action == 'IMPORT'
    if enabled and not may_enable:
        bad.append('意外启用')
    return bad


def check_realm(plan, users, realm, after_enable=False):
    approved, absent = approved_rows(plan, users)
    failures = []
    ids = {}
    for u in realm:
        if u.get('id') in ids:
            failures.append({'uuid': u.get('id'), 'problem': '导出中 UUID 重复'})
        ids[u.get('id')] = u
    people = [u for u in realm if not u.get('serviceAccount')]
    names = [u.get('username') for u in people]
    for v in {x for x in names if names.count(x) > 1}:
        failures.append({'username': mask_name(v), 'problem': '登录名重复'})
    for attr, label in (('baobei_staff_id', '员工编号'), ('baobei_subject', '业务 subject')):
        vals = [tuple((u.get('attributes') or {}).get(attr) or ()) for u in people]
        for v in {x for x in vals if x and vals.count(x) > 1}:
            failures.append({'attribute': attr, 'problem': '多个账号映射到同一%s' % label})
    planned = set()
    for p, b in approved:
        exp = expected_user(p, b)
        planned.add(exp['id'])
        u = ids.get(exp['id'])
        if u is None or u.get('serviceAccount'):
            failures.append({'staffId': b['staff_id'], 'problem': '缺失账号'})
            continue
        for problem in compare(exp, u, after_enable, p['action']):
            failures.append({'staffId': b['staff_id'], 'problem': problem})
    for b in absent:
        nid = new_realm_id(b['subject']) if UUID.match(b['subject']) else None
        login = b['work_phone'][3:] if MAINLAND.match(b.get('work_phone') or '') else None
        for u in people:
            attrs = u.get('attributes') or {}
            if u.get('id') == nid or (login and u.get('username') == login) or attrs.get('baobei_staff_id') == [b['staff_id']]:
                failures.append({'staffId': b['staff_id'], 'problem': '未批准导入的成员在新 Realm 中已有账号'})
    for u in people:
        if u.get('id') not in planned:
            failures.append({'uuid': u.get('id'), 'username': mask_name(u.get('username', '')), 'problem': '计划外的额外账号'})
    # One problem per (row, problem) only.
    uniq, seen = [], set()
    for f in failures:
        k = json.dumps(f, sort_keys=True, ensure_ascii=False)
        if k not in seen:
            seen.add(k)
            uniq.append(f)
    return approved, uniq


def build_import(args):
    users = load(args.app_users, private=True)
    out = Out(users)
    plan = load(args.plan)
    realm = load(args.realm_users)
    approved, absent = approved_rows(plan, users)
    people = [u for u in realm if not u.get('serviceAccount')]
    conflicts, present, import_users = [], [], []
    for p, b in approved:
        exp = expected_user(p, b)
        hits = [u for u in people if u.get('id') == exp['id'] or u.get('username') == exp['username']
                or (u.get('attributes') or {}).get('baobei_staff_id') == exp['attributes']['baobei_staff_id']
                or (u.get('attributes') or {}).get('baobei_subject') == exp['attributes']['baobei_subject']]
        if not hits:
            user = {'id': exp['id'], 'username': exp['username'], 'enabled': False, 'emailVerified': False,
                    'attributes': exp['attributes']}
            if exp['roles']:
                user['clientRoles'] = {'baobei-web': exp['roles']}
            # Deliberately absent: credentials, email, groups, realmRoles, federated identities.
            import_users.append(user)
            continue
        if len(hits) == 1 and hits[0].get('id') == exp['id'] and not compare(exp, hits[0], False, p['action']):
            present.append(b['staff_id'])
            continue
        conflicts.append({'staffId': b['staff_id'], 'problem': '新 Realm 中已有相同 UUID、登录名或员工编号但内容不同的账号',
                          'existing': [{'uuid': u.get('id'), 'username': mask_name(u.get('username', ''))} for u in hits]})
    for b in absent:
        login = b['work_phone'][3:] if MAINLAND.match(b.get('work_phone') or '') else None
        if any(login and u.get('username') == login or (u.get('attributes') or {}).get('baobei_staff_id') == [b['staff_id']] for u in people):
            conflicts.append({'staffId': b['staff_id'], 'problem': '未批准导入的成员在新 Realm 中已有账号'})
    if conflicts:
        out.emit({'result': 'REFUSED', 'conflicts': conflicts})
        sys.exit(2)
    write_private(args.out, json.dumps({'ifResourceExists': 'FAIL', 'users': import_users}, ensure_ascii=False, indent=2) + '\n')
    out.emit({'result': 'WRITTEN', 'file': os.path.basename(args.out), 'users': len(import_users),
              'alreadyPresentIdentical': present, 'ifResourceExists': 'FAIL'})


def verify(args):
    users = load(args.app_users, private=True)
    out = Out(users)
    approved, failures = check_realm(load(args.plan), users, load(args.realm_users), args.after_enable)
    out.emit({'checked': len(approved), 'failures': failures})
    sys.exit(1 if failures else 0)


ISSUER = re.compile(r'^https://[a-z0-9.-]+/realms/[a-z0-9-]+$')


def links(args):
    if not ISSUER.match(args.issuer):
        raise Refused('issuer must be an approved https realm issuer')
    users = load(args.app_users, private=True)
    out = Out(users)
    approved, failures = check_realm(load(args.plan), users, load(args.realm_users))
    if failures:
        out.emit({'result': 'REFUSED', 'failures': failures})
        sys.exit(1)
    q = lambda s: "'" + s.replace("'", "''") + "'"
    lines = ['BEGIN;', 'SET LOCAL lock_timeout = \'5s\';']
    for p, b in approved:
        nid = new_realm_id(b['subject'])
        # G5-RC0: re-check the live row against the approved export right before
        # inserting (a stale roster aborts the whole transaction): staff id,
        # business subject, role, status and work phone must all still match, and
        # the subject must not already be linked under this issuer.
        lines.append(
            "DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM app_users WHERE staff_id=%s AND subject=%s AND role=%s "
            "AND staff_status=%s AND work_phone=%s) "
            "OR EXISTS (SELECT 1 FROM identity_links WHERE issuer=%s AND subject=%s) THEN "
            "RAISE EXCEPTION 'roster is stale: staff/subject/role/status/phone changed or already linked'; END IF; END $$;"
            % (q(b['staff_id']), q(b['subject']), q(b['role']), q(b['staff_status']), q(b['work_phone']),
               q(args.issuer), q(b['subject'])))
        lines.append('INSERT INTO identity_links(issuer,external_subject,subject) VALUES(%s,%s,%s);' % (q(args.issuer), q(nid), q(b['subject'])))
    lines.append('COMMIT;')
    # The SQL carries the approved work phones (stale-roster check): it goes only
    # to a new private file, never to the terminal.
    write_private(args.out, '\n'.join(lines) + '\n')
    out.emit({'result': 'WRITTEN', 'file': os.path.basename(args.out), 'links': len(approved), 'transaction': 'single'})


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest='cmd', required=True)
    r = sub.add_parser('roster')
    r.add_argument('--app-users', required=True)
    r.add_argument('--keycloak-users', required=True)
    i = sub.add_parser('import')
    i.add_argument('--plan', required=True)
    i.add_argument('--app-users', required=True)
    i.add_argument('--realm-users', required=True, help='current dedicated-realm export (may be [])')
    i.add_argument('--out', required=True, help='new private file for the partialImport JSON')
    v = sub.add_parser('verify')
    v.add_argument('--plan', required=True)
    v.add_argument('--app-users', required=True)
    v.add_argument('--realm-users', required=True)
    v.add_argument('--after-enable', action='store_true')
    lk = sub.add_parser('links')
    lk.add_argument('--plan', required=True)
    lk.add_argument('--app-users', required=True)
    lk.add_argument('--realm-users', required=True)
    lk.add_argument('--issuer', required=True)
    lk.add_argument('--out', required=True, help='new private file (0600) for the guarded link SQL')
    args = ap.parse_args()
    try:
        {'roster': roster, 'import': build_import, 'verify': verify, 'links': links}[args.cmd](args)
    except Refused as e:
        print(json.dumps({'result': 'REFUSED', 'reason': str(e)}, ensure_ascii=False), file=sys.stderr)
        sys.exit(2)


if __name__ == '__main__':
    main()
