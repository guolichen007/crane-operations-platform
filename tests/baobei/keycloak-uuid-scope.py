"""Minimal check: can the same user UUID exist in two realms of one Keycloak server?"""
import json, os, secrets, sys, importlib.util
HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location('probe', os.path.join(HERE, 'keycloak-realm-probe.py'))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)
call, admin_token = probe.call, probe.admin_token
adm = admin_token()
fresh = '0000aaaa-%04x-4000-8000-%012x' % (secrets.randbelow(65535), secrets.randbelow(2**48))
s, b, _ = call('POST', '/admin/realms/baobei/partialImport', {'ifResourceExists': 'SKIP', 'users': [{'id': fresh, 'username': 'uuid-fresh-' + fresh[-6:], 'enabled': False}]}, adm)
print('A. unused UUID into baobei:', s, b if s != 200 else 'ok')
s, b, h = call('POST', '/admin/realms/crane/users', {'username': 'uuid-shared-' + secrets.token_hex(3), 'enabled': True}, adm)
crane_id = h['Location'].rsplit('/', 1)[-1]
s, b, _ = call('POST', '/admin/realms/baobei/partialImport', {'ifResourceExists': 'SKIP', 'users': [{'id': crane_id, 'username': 'uuid-copy-' + crane_id[-6:], 'enabled': False}]}, adm)
print('B. UUID already used in crane realm, imported into baobei:', s, b)
s, b, _ = call('POST', '/admin/realms/baobei/partialImport', {'ifResourceExists': 'OVERWRITE', 'users': [{'id': crane_id, 'username': 'uuid-copy-' + crane_id[-6:], 'enabled': False}]}, adm)
print('C. same with OVERWRITE (diagnostic only):', s, b)
s, u, _ = call('GET', f'/admin/realms/crane/users/{crane_id}', token=adm)
print('   crane user still intact:', s, u.get('username') if isinstance(u, dict) else u)
s, b, _ = call('POST', '/admin/realms/baobei/users', {'id': crane_id, 'username': 'uuid-rest-' + crane_id[-6:], 'enabled': False}, adm)
print('D. admin REST create with explicit id:', s, b)
s, l, _ = call('GET', '/admin/realms/baobei/users?username=uuid-rest-' + crane_id[-6:], token=adm)
print('   created id equals requested:', [x['id'] == crane_id for x in l] if isinstance(l, list) else l)
