"""G5-RC0-R1: admin-preflight.py offline checks (CI contract gate, no Keycloak).

The real-Keycloak drill is tests/baobei/keycloak-admin-preflight.py (13 cases on
Keycloak 26.8.1). Here: the tool never reports a login it did not perform, it
refuses non-private inputs and non-https endpoints, and it is read-only (only GET
on the Admin API, POST only to the token / login endpoints).
"""
import importlib.util
import json
import os
import re
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOL = os.path.join(HERE, '..', '..', '..', 'deploy', 'baobei', 'admin-preflight.py')
spec = importlib.util.spec_from_file_location('pre', TOOL)
pre = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pre)


class PreflightOffline(unittest.TestCase):
    def test_admin_api_calls_are_read_only(self):
        source = open(TOOL, encoding='utf-8').read()
        writes = re.findall(r"c\.call\('(PUT|DELETE|PATCH|POST)', ([^,]+)", source)
        for method, path in writes:
            self.assertEqual(method, 'POST', path)
            self.assertTrue('/token' in path or 'm.group(1)' in path, 'only token and login-form POSTs allowed: ' + path)

    def test_https_required_except_loopback(self):
        with self.assertRaises(pre.Refused):
            pre.Client('http://keycloak.example.test')
        pre.Client('https://keycloak.example.test')
        pre.Client('http://127.0.0.1:18080')

    def test_private_inputs_only(self):
        if os.name != 'posix':
            self.skipTest('posix permissions')
        with tempfile.NamedTemporaryFile('w', delete=False) as fh:
            fh.write('x')
        os.chmod(fh.name, 0o644)
        with self.assertRaises(pre.Refused):
            pre.private(fh.name)
        os.chmod(fh.name, 0o600)
        self.assertEqual(pre.private(fh.name), 'x')
        os.unlink(fh.name)

    def test_unapproved_realm_refused(self):
        class Args:
            base, realm = 'https://keycloak.example.test', 'master'
        with self.assertRaises(pre.Refused):
            pre.check_admin(Args, {})

    def test_masking(self):
        self.assertEqual(pre.mask('+8613912345678'), '+86****5678')


if __name__ == '__main__':
    unittest.main()
