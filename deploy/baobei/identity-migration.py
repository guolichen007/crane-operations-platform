#!/usr/bin/env python3
"""G4-S5-R1-E identity migration tools (server CC runs these; no production writes).

  roster   READ-ONLY. Joins the Baobei app_users export with the shared-realm user
           export and classifies every row. Prints a masked, auditable plan.
  import   Builds the minimal partialImport file for the dedicated realm from an
           APPROVED plan: only approved users, disabled, no credentials, no sessions,
           only baobei-web client roles, ifResourceExists=SKIP. Keycloak user ids are
           unique per server (USER_ENTITY primary key, verified on 26.8.1), so while
           the shared-realm account exists the new user cannot reuse its UUID. Each
           user therefore gets a new, deterministic UUID (UUIDv5 of the business
           subject) and carries baobei_subject=<original UUID>; the Baobei database
           maps it back with identity_links. The business subject, projects,
           approvals and audit rows never change.
  verify   Compares the dedicated realm (admin export) with the approved plan:
           new UUID, link attribute, username, enabled=false, exact single role,
           no other roles/groups, no credentials.
  links    Prints the identity_links rows (issuer, new UUID, business subject) for
           the controlled link step; SQL is applied by the operator after verify.

Inputs are JSON files produced by read-only commands, for example
  psql -At -c "SELECT json_agg(...) FROM app_users"   (see docs/项目报备中心/41 号)
  kcadm.sh get users -r crane --fields id,username,enabled,email,attributes
  kcadm.sh get users/<id>/role-mappings -r crane
No secret is read or written. Output never contains phone numbers in full.
"""
import argparse
import json
import re
import sys
import uuid as uuidlib

# Namespace for deterministic new-realm user ids (UUIDv5 of the business subject).
LINK_NS = uuidlib.UUID('7b3c0b1e-3f4e-4b5a-9a7e-6c2d1f0e9b81')


def new_realm_id(subject):
    return str(uuidlib.uuid5(LINK_NS, 'baobei:' + subject))

UUID = re.compile(r'^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$')
MAINLAND = re.compile(r'^\+861[3-9]\d{9}$')
SYNTHETIC = re.compile(r'^(synthetic-|preview-|g[0-9]|fixture)', re.I)
ROLES = {'baobei_sales', 'baobei_manager', 'baobei_director', 'baobei_admin'}


def mask(phone):
    return (phone[:-8] + '****' + phone[-4:]) if phone and len(phone) >= 8 else (phone or '')


def load(path):
    with open(path, encoding='utf-8') as fh:
        return json.load(fh)


def classify(row, kc_by_id):
    """One business row → (category, action, notes)."""
    subject = row['subject']
    status = row['staff_status']
    refs = sum(int(row.get(k) or 0) for k in ('projects', 'created', 'approvals', 'participations', 'audit', 'activities'))
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
    if status == 'SUSPENDED':
        return 'SUSPENDED', 'IMPORT_DISABLED', notes
    phone = row.get('work_phone') or ''
    if not phone:
        notes.append('缺少工作手机：不得猜测，需管理员补全后才能设置登录手机号')
    elif not MAINLAND.match(phone):
        notes.append('非中国大陆手机号：当前登录只支持 11 位大陆手机')
    roles = set(kc.get('baobeiRoles') or [])
    if roles != {row['role']}:
        notes.append('旧 Realm 角色与业务角色不一致：%s' % sorted(roles))
    category = 'ADMIN' if row['role'] == 'baobei_admin' else 'STAFF'
    action = 'IMPORT' if phone and MAINLAND.match(phone) and roles == {row['role']} else 'IMPORT_BLOCKED'
    return category, action, notes


def roster(args):
    users = load(args.app_users)
    kc_users = load(args.keycloak_users)
    kc_by_id = {u['id']: u for u in kc_users}
    plan = []
    for row in sorted(users, key=lambda r: (r['role'], r['display_name'])):
        category, action, notes = classify(row, kc_by_id)
        kc = kc_by_id.get(row['subject'], {})
        plan.append({
            'staffId': row['staff_id'], 'expectedUuid': row['subject'], 'displayName': row['display_name'],
            'role': row['role'], 'status': row['staff_status'], 'category': category, 'action': action,
            'phone': mask(row.get('work_phone')), 'phoneReady': bool(row.get('work_phone') and MAINLAND.match(row['work_phone'])),
            'oldUsername': kc.get('username', ''), 'oldEnabled': kc.get('enabled'),
            'references': {k: int(row.get(k) or 0) for k in ('projects', 'created', 'approvals', 'participations', 'audit', 'activities')},
            'notes': notes,
        })
    counts = {}
    for p in plan:
        counts[p['action']] = counts.get(p['action'], 0) + 1
    unmatched_kc = [u for u in kc_users if u['id'] not in {r['subject'] for r in users} and u.get('baobeiRoles')]
    out = {'businessRows': len(users), 'keycloakUsersWithBaobeiRole': sum(1 for u in kc_users if u.get('baobeiRoles')),
           'actions': counts, 'keycloakOnly': [{'uuid': u['id'], 'username': u['username'], 'roles': u['baobeiRoles']} for u in unmatched_kc],
           'plan': plan}
    json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
    print()


def build_import(args):
    plan = load(args.plan)
    phones = load(args.phones)  # {staffId: "+8613..."} — taken from app_users, never guessed
    users = []
    seen = set()
    for p in plan['plan']:
        if p['action'] not in ('IMPORT', 'IMPORT_DISABLED', 'IMPORT_DISABLED_KEEP_HISTORY') or not p.get('approved'):
            continue
        if not UUID.match(p['expectedUuid']) or p['role'] not in ROLES:
            sys.exit('plan row rejected: ' + p['staffId'])
        phone = phones.get(p['staffId'], '')
        if not MAINLAND.match(phone):
            sys.exit('approved row without a mainland phone: ' + p['staffId'])
        username = phone[3:]
        if username in seen:
            sys.exit('duplicate login phone in plan')
        seen.add(username)
        users.append({
            'id': new_realm_id(p['expectedUuid']), 'username': username, 'enabled': False, 'emailVerified': False,
            'attributes': {'baobei_staff_id': [p['staffId']], 'baobei_subject': [p['expectedUuid']]},
            'clientRoles': {'baobei-web': [p['role']]},
            # Deliberately absent: credentials, email, groups, realmRoles, federated identities.
        })
    json.dump({'ifResourceExists': 'SKIP', 'users': users}, sys.stdout, ensure_ascii=False, indent=2)
    print()


def verify(args):
    plan = {new_realm_id(p['expectedUuid']): p for p in load(args.plan)['plan'] if p.get('approved') and p['action'].startswith('IMPORT')}
    realm = {u['id']: u for u in load(args.realm_users)}
    failures = []
    for uuid, p in plan.items():
        u = realm.get(uuid)
        if not u:
            failures.append((uuid, 'missing'))
            continue
        roles = set(u.get('baobeiRoles') or [])
        other = set(u.get('otherRoles') or []) | set(u.get('groups') or [])
        if roles != {p['role']} or other:
            failures.append((uuid, 'roles %s other %s' % (sorted(roles), sorted(other))))
        if u.get('enabled') and not args.after_enable:
            failures.append((uuid, 'enabled before verification'))
        if u.get('credentials'):
            failures.append((uuid, 'carries credentials'))
        if (u.get('attributes') or {}).get('baobei_subject') != [p['expectedUuid']]:
            failures.append((uuid, 'link attribute does not name the business subject'))
    extra = [uuid for uuid in realm if uuid not in plan and not realm[uuid].get('serviceAccount')]
    for uuid in extra:
        failures.append((uuid, 'not in the approved plan'))
    print(json.dumps({'checked': len(plan), 'failures': failures}, ensure_ascii=False, indent=2))
    sys.exit(1 if failures else 0)


def links(args):
    issuer = args.issuer
    if not re.match(r'^https://[a-z0-9.-]+/realms/[a-z0-9-]+$', issuer):
        sys.exit('issuer must be an approved https realm issuer')
    for p in load(args.plan)['plan']:
        if p.get('approved') and p['action'].startswith('IMPORT') and p['action'] != 'IMPORT_BLOCKED':
            print(json.dumps({'issuer': issuer, 'external_subject': new_realm_id(p['expectedUuid']), 'subject': p['expectedUuid']}))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest='cmd', required=True)
    r = sub.add_parser('roster')
    r.add_argument('--app-users', required=True)
    r.add_argument('--keycloak-users', required=True)
    i = sub.add_parser('import')
    i.add_argument('--plan', required=True)
    i.add_argument('--phones', required=True)
    v = sub.add_parser('verify')
    v.add_argument('--plan', required=True)
    v.add_argument('--realm-users', required=True)
    v.add_argument('--after-enable', action='store_true')
    lk = sub.add_parser('links')
    lk.add_argument('--plan', required=True)
    lk.add_argument('--issuer', required=True)
    args = ap.parse_args()
    {'roster': roster, 'import': build_import, 'verify': verify, 'links': links}[args.cmd](args)


if __name__ == '__main__':
    main()
