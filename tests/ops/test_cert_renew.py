"""Root-only Linux fixtures. Real ephemeral PEM crypto; fake ACME, ingress and public network.
No request reaches a CA or production service. All private keys live in temporary directories.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[2]
DOMAIN = 'crane.youdaozd.com'
LINUX_ROOT = os.name == 'posix' and hasattr(os, 'geteuid') and os.geteuid() == 0

FAKE = r'''#!/usr/bin/python3
import json, os, pathlib, shutil, subprocess, sys, time
base = pathlib.Path(os.environ['CERT_FIXTURE'])
kind = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with (base/'calls').open('a') as stream: stream.write(json.dumps([kind]+args)+'\n')
scenario = os.environ.get('SCENARIO', '')
def count(name):
    p=base/name; n=int(p.read_text())+1 if p.exists() else 1; p.write_text(str(n)); return n
if kind == 'sleep': sys.exit(0)
if kind == 'chmod':
    if scenario=='marker-chmod' and args[-1].endswith('/.production-adopted'): sys.exit(1)
    sys.exit(subprocess.call(['/bin/chmod']+args))
if kind == 'acme.sh':
    assert '--server' in args and args[args.index('--server')+1]=='letsencrypt'
    assert '--force' not in args and '--auto-upgrade' not in args
    assert os.environ['ACME_PACKAGED']=='1'
    if scenario=='concurrent': (base/'entered').touch(); time.sleep(2)
    if '--issue' in args: sys.exit(0)
    if '--cron' in args and scenario=='noop': sys.exit(0)
    stage=pathlib.Path(os.environ['ACME_HOME'])/'deploy'/os.environ['FIXTURE_DOMAIN']
    for name in ['fullchain.pem','privkey.pem']: shutil.copyfile(base/'new'/name,stage/name)
    if '--install-cert' in args:
        folder=pathlib.Path(os.environ['ACME_HOME'])/'certs'/os.environ['FIXTURE_DOMAIN']; folder.mkdir(parents=True,exist_ok=True)
        values={'Le_Domain':os.environ['FIXTURE_DOMAIN'],'Le_Alt':'no','Le_Keylength':'2048','Le_Webroot':os.environ['WEBROOT'],'Le_API':'https://acme-v02.api.letsencrypt.org/directory','Le_RealKeyPath':str(stage/'privkey.pem'),'Le_RealFullChainPath':str(stage/'fullchain.pem'),'Le_ReloadCmd':''}
        (folder/(os.environ['FIXTURE_DOMAIN']+'.conf')).write_text('\n'.join(k+'='+repr(v) for k,v in values.items()))
    # Deliberate confidential upstream noise must never reach operator logs.
    print('PRIVATE_KEY_SENTINEL SESSION_SECRET_SENTINEL password=DO_NOT_LOG')
    sys.exit(0)
if kind == 'docker':
    assert args[:2]==['exec','u-dao-ingress-nginx-1']
    assert args[2:] in [['nginx','-t'],['nginx','-s','reload']]
    if args[-1]=='-t':
        n=count('nginx-tests')
        sys.exit(1 if (scenario=='precheck' and n==1) or (scenario=='nginx-after' and n==2) or (scenario=='rollback-critical' and n>=2) else 0)
    n=count('reloads')
    sys.exit(1 if scenario=='reload' and n==1 else 0)
if kind == 'openssl':
    if args[0]!='s_client': sys.exit(subprocess.call(['/usr/bin/openssl']+args))
    assert '-verify_return_error' in args and '-verify_hostname' in args and '-servername' in args
    assert args[args.index('-verify_hostname')+1]==os.environ['FIXTURE_DOMAIN']
    current=(pathlib.Path(os.environ['PRODUCTION_TLS'])/'fullchain.pem').read_bytes()
    is_new=current==(base/'new/fullchain.pem').read_bytes()
    if scenario=='remote-chain' and is_new: sys.exit(1)
    if scenario=='remote-fingerprint' and is_new: current=(base/'old/fullchain.pem').read_bytes()
    sys.stdout.buffer.write(current); sys.exit(0)
if kind == 'curl':
    assert args[0]=='--disable'  # Ignore ambient root curlrc (TLS bypass, auth or logging).
    assert '-k' not in args and '--insecure' not in args
    url=next(x for x in args if x.startswith('http'))
    out=pathlib.Path(args[args.index('-o')+1])
    if url.startswith('http:'):
        if scenario=='probe': sys.exit(22)
        out.write_text(url.rsplit('/',1)[1] if scenario!='probe-body' else 'wrong')
        print('127.0.0.1' if scenario=='probe-private' else '8.8.8.8',end=''); sys.exit(0)
    is_new=(pathlib.Path(os.environ['PRODUCTION_TLS'])/'fullchain.pem').read_bytes()==(base/'new/fullchain.pem').read_bytes()
    if is_new and ((scenario=='crane' and url.endswith('crane.youdaozd.com/')) or (scenario=='firebot' and 'firebot.' in url) or (scenario=='firebot-health' and '/health/live' in url)): sys.exit(22)
    out.write_text(json.dumps({'issuer':'wrong' if scenario=='oidc' and is_new else 'https://crane.youdaozd.com/realms/crane'}) if 'openid-configuration' in url else 'ok')
    sys.exit(0)
raise RuntimeError('unexpected fake command')
'''


@unittest.skipUnless(LINUX_ROOT, 'certificate lifecycle requires isolated Linux root fixture')
class CertificateLifecycle(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.material = tempfile.TemporaryDirectory(prefix='crane-cert-material-')
        cls.m = Path(cls.material.name)
        cls.crypto('req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '90', '-subj', "/O=Let's Encrypt/CN=Anonymous Fixture CA", '-keyout', str(cls.m/'ca.key'), '-out', str(cls.m/'ca.pem'))
        for name, domain, days in [('old', DOMAIN, '30'), ('new', DOMAIN, '30'), ('wrong', 'other.example.test', '30'), ('short', DOMAIN, '1'), ('expired', DOMAIN, '-1')]:
            cls.crypto('req', '-new', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN='+domain, '-keyout', str(cls.m/(name+'.key')), '-out', str(cls.m/(name+'.csr')))
            ext=cls.m/(name+'.ext'); ext.write_text('subjectAltName=DNS:'+domain+'\nextendedKeyUsage=serverAuth\n')
            cls.crypto('x509', '-req', '-in', str(cls.m/(name+'.csr')), '-CA', str(cls.m/'ca.pem'), '-CAkey', str(cls.m/'ca.key'), '-CAcreateserial', '-days', days, '-extfile', str(ext), '-out', str(cls.m/(name+'.pem')))

    @classmethod
    def tearDownClass(cls): cls.material.cleanup()

    @staticmethod
    def crypto(*args): subprocess.run(['/usr/bin/openssl', *args], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='crane-cert-test-'); self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name)
        for name in ['home','web','prod','old','new','bin']: (self.root/name).mkdir(mode=0o700)
        self.stage=self.root/'home/deploy'/DOMAIN; self.stage.mkdir(parents=True,mode=0o700)
        (self.root/'home/.renew.lock').touch(mode=0o600)
        for target, material in [('old','old'),('prod','old'),('new','new')]:
            shutil.copyfile(self.m/(material+'.pem'),self.root/target/'fullchain.pem')
            shutil.copyfile(self.m/(material+'.key'),self.root/target/'privkey.pem')
        for name in ['acme.sh','docker','openssl','curl','sleep','chmod']:
            path=self.root/'bin'/name; path.write_text(FAKE); path.chmod(0o755)
        self.env={**os.environ,'PATH':str(self.root/'bin')+':'+os.environ['PATH'],'CERT_FIXTURE':str(self.root),'FIXTURE_DOMAIN':DOMAIN,'ACME_HOME':str(self.root/'home'),'WEBROOT':str(self.root/'web'),'PRODUCTION_TLS':str(self.root/'prod'),'ACME_BIN':str(self.root/'bin/acme.sh'),'SSL_CERT_FILE':str(self.m/'ca.pem')}
        self.run_mode('--bootstrap', success=True)
        (self.root/'calls').write_text('')

    def run_mode(self, mode, scenario='', success=False):
        r=subprocess.run(['bash',str(ROOT/'deploy/scripts/cert-renew.sh'),mode],env={**self.env,'SCENARIO':scenario},capture_output=True,text=True,timeout=30)
        self.assertNotIn('PRIVATE_KEY_SENTINEL',r.stdout+r.stderr)
        self.assertNotIn('SESSION_SECRET_SENTINEL',r.stdout+r.stderr)
        self.assertNotIn('BEGIN PRIVATE KEY',r.stdout+r.stderr)
        self.assertNotIn('BEGIN CERTIFICATE',r.stdout+r.stderr)
        self.assertNotIn('DO_NOT_LOG',r.stdout+r.stderr)
        if success: self.assertEqual(r.returncode,0,r.stdout+r.stderr)
        return r

    def calls(self): return [json.loads(line) for line in (self.root/'calls').read_text().splitlines()]
    def adopted(self): (self.root/'home/.production-adopted').touch(mode=0o600)
    def assert_old(self):
        for name in ['fullchain.pem','privkey.pem']: self.assertEqual((self.root/'prod'/name).read_bytes(),(self.root/'old'/name).read_bytes())

    def test_no_new_certificate_is_noop_without_reload(self):
        self.run_mode('--adopt',success=True)
        (self.root/'calls').write_text('')
        r=self.run_mode('--run','noop',success=True)
        self.assertIn('CERT_RENEW_RESULT=NOOP',r.stdout)
        self.assertFalse(any(c[0]=='docker' for c in self.calls()))

    def test_probe_failure_never_requests_ca(self):
        for scenario in ['probe','probe-body','probe-private']:
            (self.root/'calls').write_text('')
            r=self.run_mode('--bootstrap',scenario)
            self.assertNotEqual(r.returncode,0)
            self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))
            self.assert_old()

    def test_invalid_candidate_refused(self):
        for scenario in ['wrong','short','expired','mismatch']:
            with self.subTest(candidate=scenario):
                material='new' if scenario=='mismatch' else scenario
                shutil.copyfile(self.m/(material+'.pem'),self.stage/'fullchain.pem')
                shutil.copyfile(self.m/('old.key' if scenario=='mismatch' else material+'.key'),self.stage/'privkey.pem')
                r=self.run_mode('--adopt')
                self.assertNotEqual(r.returncode,0)
                self.assertFalse((self.root/'home/.production-adopted').exists())
                self.assert_old()

    def test_unadopted_schedule_cannot_renew_or_promote(self):
        self.run_mode('--run',success=True)
        self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))
        self.assert_old()

    def test_certificate_file_cannot_publish_embedded_private_key(self):
        with (self.stage/'fullchain.pem').open('ab') as stream: stream.write((self.stage/'privkey.pem').read_bytes())
        r=self.run_mode('--adopt')
        self.assertNotEqual(r.returncode,0); self.assert_old()
        self.assertFalse((self.root/'home/.production-adopted').exists())

    def test_scheduled_cron_promotes_new_candidate(self):
        self.adopted()
        r=self.run_mode('--run',success=True)
        self.assertIn('CERT_RENEW_RESULT=PROMOTED',r.stdout)
        self.assertTrue(any(c[0]=='acme.sh' and '--cron' in c for c in self.calls()))

    def test_expiring_current_certificate_does_not_block_valid_candidate(self):
        for name in ['short','expired']:
            with self.subTest(current=name):
                (self.root/'home/.production-adopted').unlink(missing_ok=True)
                shutil.copyfile(self.m/(name+'.pem'),self.root/'prod/fullchain.pem')
                shutil.copyfile(self.m/(name+'.key'),self.root/'prod/privkey.pem')
                r=self.run_mode('--adopt',success=True)
                self.assertIn('CERT_RENEW_RESULT=PROMOTED',r.stdout)

    def test_failed_marker_creation_cannot_authorize_a_later_schedule(self):
        r=self.run_mode('--adopt','marker-chmod')
        self.assertNotEqual(r.returncode,0); self.assert_old()
        self.assertFalse((self.root/'home/.production-adopted').exists())
        self.assertFalse(any(c[0]=='docker' for c in self.calls()))

    def test_nginx_precheck_never_changes_production(self):
        r=self.run_mode('--adopt','precheck')
        self.assertNotEqual(r.returncode,0); self.assert_old()
        self.assertFalse((self.root/'home/.production-adopted').exists())
        self.assertFalse((self.root/'reloads').exists())

    def test_all_promotion_failure_paths_restore_and_recheck_old_service(self):
        for scenario in ['nginx-after','reload','crane','oidc','firebot','firebot-health','remote-fingerprint','remote-chain']:
            with self.subTest(scenario=scenario):
                for f in ['nginx-tests','reloads']: (self.root/f).unlink(missing_ok=True)
                r=self.run_mode('--adopt',scenario)
                self.assertNotEqual(r.returncode,0)
                self.assertIn('CERT_RENEW_RESULT=ROLLED_BACK',r.stdout,r.stdout+r.stderr)
                self.assert_old(); self.assertFalse((self.root/'home/.production-adopted').exists())
                self.assertFalse(list((self.root/'home/deploy').glob('.rollback.*')))
                self.assertTrue(any(c[0]=='curl' and 'https://firebot.youdaozd.com/health/live' in c for c in self.calls()))

    def test_rollback_failure_is_critical_and_preserves_private_recovery_copy(self):
        r=self.run_mode('--adopt','rollback-critical')
        self.assertNotEqual(r.returncode,0); self.assertIn('CERT_RENEW_RESULT=CRITICAL',r.stdout)
        backups=list((self.root/'home/deploy').glob('.rollback.*')); self.assertEqual(len(backups),1)
        self.assertEqual(backups[0].stat().st_mode & 0o777,0o700)
        self.assertTrue((self.root/'home/.renew-critical').exists())
        (self.root/'calls').write_text('')
        r=self.run_mode('--run'); self.assertNotEqual(r.returncode,0); self.assertIn('CERT_RENEW_RESULT=CRITICAL',r.stdout)
        self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))

    def test_success_promotes_permissions_and_check_is_readonly(self):
        r=self.run_mode('--adopt',success=True); self.assertIn('CERT_RENEW_RESULT=PROMOTED',r.stdout)
        self.assertTrue((self.root/'home/.production-adopted').exists())
        self.assertEqual((self.root/'prod/privkey.pem').stat().st_mode & 0o777,0o600)
        self.assertEqual((self.root/'prod/fullchain.pem').stat().st_mode & 0o777,0o644)
        (self.root/'calls').write_text('')
        self.run_mode('--check',success=True)
        self.assertFalse(any(c[0] in ['acme.sh','docker'] for c in self.calls()))

    def test_concurrent_run_is_refused_by_real_flock(self):
        self.adopted()
        p=subprocess.Popen(['bash',str(ROOT/'deploy/scripts/cert-renew.sh'),'--run'],env={**self.env,'SCENARIO':'concurrent'},stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
        try:
            for _ in range(100):
                if (self.root/'entered').exists(): break
                time.sleep(.03)
            self.assertTrue((self.root/'entered').exists())
            r=self.run_mode('--run'); self.assertNotEqual(r.returncode,0); self.assertIn('CERT_RENEW_RESULT=LOCKED',r.stdout)
            p.communicate(timeout=30); self.assertEqual(p.returncode,0)
        finally:
            if p.poll() is None: p.kill(); p.wait()

    def test_saved_hook_or_production_destination_is_rejected_before_acme(self):
        self.adopted()
        conf=self.root/'home/certs'/DOMAIN/(DOMAIN+'.conf')
        original=conf.read_text()
        for extra in ["\nLe_ReloadCmd='SECRET_HOOK'", "\nLe_RealKeyPath='/production/private.key'", "\nLe_Alt='*.example.test'", "\ntouch /tmp/must-not-execute"]:
            conf.write_text(original+extra); (self.root/'calls').write_text('')
            r=self.run_mode('--run'); self.assertNotEqual(r.returncode,0)
            self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))
        conf.write_text(original)


if __name__ == '__main__': unittest.main()
