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
import importlib.util
from unittest.mock import patch

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
if kind == 'cert-state.py':
    if scenario=='marker-chmod' and args[2]=='adopt': sys.exit(1)
    sys.exit(subprocess.call(['/usr/bin/python3',os.environ['REAL_STATE_TOOL']]+args))
if kind == 'chmod':
    if scenario=='marker-chmod' and args[-1].endswith('/.production-adopted'): sys.exit(1)
    sys.exit(subprocess.call(['/bin/chmod']+args))
if kind == 'acme.sh':
    assert '--server' in args and args[args.index('--server')+1]=='letsencrypt'
    assert '--force' not in args and '--auto-upgrade' not in args
    assert os.environ['ACME_PACKAGED']=='1'
    if scenario.startswith('acme-error-'):
        print({'network':'connect timeout','CA':'urn:ietf:params:acme:error:rateLimited','challenge':'challenge invalid response'}[scenario[11:]]+' PRIVATE_KEY_SENTINEL password=DO_NOT_LOG')
        sys.exit(17)
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
        for name, domain, days in [('old', DOMAIN, '30'), ('new', DOMAIN, '30'), ('wrong', 'other.example.test', '30'), ('short', DOMAIN, '1'), ('expired', DOMAIN, '-1'), ('six', DOMAIN, '6'), ('above', DOMAIN, '8')]:
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
        self.stage.parent.chmod(0o700)  # same root-only deploy directory as provision
        (self.root/'home/.renew.lock').touch(mode=0o600)
        for target, material in [('old','old'),('prod','old'),('new','new')]:
            shutil.copyfile(self.m/(material+'.pem'),self.root/target/'fullchain.pem')
            shutil.copyfile(self.m/(material+'.key'),self.root/target/'privkey.pem')
        for name in ['acme.sh','docker','openssl','curl','sleep','chmod','cert-state.py']:
            path=self.root/'bin'/name; path.write_text(FAKE); path.chmod(0o755)
        self.env={**os.environ,'PATH':str(self.root/'bin')+':'+os.environ['PATH'],'CERT_FIXTURE':str(self.root),'FIXTURE_DOMAIN':DOMAIN,'ACME_HOME':str(self.root/'home'),'WEBROOT':str(self.root/'web'),'PRODUCTION_TLS':str(self.root/'prod'),'ACME_BIN':str(self.root/'bin/acme.sh'),'SSL_CERT_FILE':str(self.m/'ca.pem'),'CERT_STATE_TOOL':str(ROOT/'deploy/scripts/cert-state.py'),'REAL_STATE_TOOL':str(ROOT/'deploy/scripts/cert-state.py')}
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
        self.assertTrue(any(c[0]=='curl' and 'https://firebot.youdaozd.com/health/live' in c for c in self.calls()))

    def test_probe_failure_never_requests_ca(self):
        for scenario in ['probe','probe-body','probe-private']:
            (self.root/'calls').write_text('')
            r=self.run_mode('--bootstrap',scenario)
            self.assertNotEqual(r.returncode,0)
            self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))
            self.assert_old()

    def test_invalid_candidate_refused(self):
        for scenario in ['wrong','short','expired','six','mismatch']:
            with self.subTest(candidate=scenario):
                material='new' if scenario=='mismatch' else scenario
                shutil.copyfile(self.m/(material+'.pem'),self.stage/'fullchain.pem')
                shutil.copyfile(self.m/('old.key' if scenario=='mismatch' else material+'.key'),self.stage/'privkey.pem')
                r=self.run_mode('--adopt')
                self.assertNotEqual(r.returncode,0)
                self.assertFalse((self.root/'home/.production-adopted').exists())
                self.assert_old()
                entries=[json.loads(line) for line in (self.root/'home/cert-diagnostics.jsonl').read_text().splitlines()]
                self.assertEqual(entries[-1]['reason'],'validation')

    def test_unadopted_schedule_cannot_renew_or_promote(self):
        self.run_mode('--run',success=True)
        self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))
        self.assert_old()

    def test_overlapping_state_webroot_or_production_paths_refuse_before_ca(self):
        original=self.env.copy()
        for key, value in [('PRODUCTION_TLS',str(self.stage)),('PRODUCTION_TLS',str(self.root/'home')),('WEBROOT',str(self.root/'home'))]:
            with self.subTest(key=key,value=value):
                self.env={**original,key:value}; (self.root/'calls').write_text('')
                r=self.run_mode('--bootstrap'); self.assertNotEqual(r.returncode,0)
                self.assertFalse(any(c[0]=='acme.sh' for c in self.calls())); self.assert_old()
        self.env=original

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
        self.env['CERT_STATE_TOOL']=str(self.root/'bin/cert-state.py')
        r=self.run_mode('--adopt','marker-chmod')
        self.assertNotEqual(r.returncode,0)
        self.assertFalse((self.root/'home/.production-adopted').exists())
        self.assertFalse((self.root/'home/.promotion-pending').exists())
        self.assertEqual((self.root/'prod/fullchain.pem').read_bytes(),(self.root/'new/fullchain.pem').read_bytes())
        (self.root/'calls').write_text('')
        self.run_mode('--run',success=True)
        self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))
        self.run_mode('--adopt',success=True)
        self.assertTrue((self.root/'home/.production-adopted').exists())
        self.assertFalse(any(c[0] in ['docker','acme.sh'] for c in self.calls()))

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
        r=self.run_mode('--run'); self.assertNotEqual(r.returncode,0); self.assertIn('CERT_RENEW_RESULT=RECOVERED_ROLLBACK',r.stdout)
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
            entries=[json.loads(line) for line in (self.root/'home/cert-diagnostics.jsonl').read_text().splitlines()]
            self.assertEqual(entries[-1]['reason'],'state-policy')
        conf.write_text(original)

    def fp(self, path):
        return subprocess.check_output(['/usr/bin/openssl','x509','-in',str(path),'-noout','-fingerprint','-sha256'],text=True).strip().split('=')[1].replace(':','')

    def state(self, *args):
        return subprocess.run(['/usr/bin/python3',str(ROOT/'deploy/scripts/cert-state.py'),str(self.root/'home'),str(self.root/'prod'),*args],capture_output=True,text=True,check=True)

    def interrupted(self, partial=False, adopted=False):
        backup=self.root/'home/deploy/.rollback.ABC123'; backup.mkdir(mode=0o700)
        for file in ['fullchain.pem','privkey.pem']:
            shutil.copyfile(self.root/'old'/file,backup/file); (backup/file).chmod(0o600)
        if adopted: self.adopted()
        self.state('create',backup.name,self.fp(self.root/'old/fullchain.pem'),self.fp(self.root/'new/fullchain.pem'),'--run' if adopted else '--adopt','1' if adopted else '0')
        for file in ['fullchain.pem'] if partial else ['fullchain.pem','privkey.pem']: shutil.copyfile(self.root/'new'/file,self.root/'prod'/file)
        return backup

    def assert_recovered(self, mode='--run', scenario=''):
        r=self.run_mode(mode,scenario)
        self.assertEqual(r.returncode,3,r.stdout+r.stderr); self.assertIn('RECOVERED_ROLLBACK',r.stdout)
        self.assert_old(); self.assertFalse((self.root/'home/.promotion-pending').exists())
        self.assertFalse(list((self.root/'home/deploy').glob('.rollback.*')))
        self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))
        return r

    def test_partial_pair_pending_recovers_before_ca(self):
        self.interrupted(partial=True); self.assert_recovered()
        self.assertFalse((self.root/'home/.production-adopted').exists())

    def test_complete_new_pair_remote_old_recovers(self):
        self.interrupted(); self.assert_recovered(scenario='remote-fingerprint')

    def test_reloaded_or_health_complete_pending_still_conservatively_recovers(self):
        self.interrupted()
        self.run_mode('--check',success=True)  # new pair is healthy but transaction is not committed
        self.assertTrue((self.root/'home/.promotion-pending').exists())
        (self.root/'calls').write_text('')
        self.assert_recovered('--adopt')

    def test_malformed_missing_or_escaping_pending_never_calls_ca(self):
        for scenario in ['malformed','escape','missing','symlink','mode']:
            with self.subTest(state=scenario):
                self.setUp()
                backup=self.interrupted(partial=True)
                pending=self.root/'home/.promotion-pending'
                outside=self.root/'outside'; outside.mkdir(mode=0o700); (outside/'secret').write_text('DO_NOT_LOG')
                data=json.loads(pending.read_text())
                if scenario=='malformed': pending.write_text('{invalid')
                elif scenario=='escape': data['rollback']=str(outside); pending.write_text(json.dumps(data))
                elif scenario=='missing': shutil.rmtree(backup)
                elif scenario=='symlink': pending.unlink(); pending.symlink_to(outside/'secret')
                else: pending.chmod(0o644)
                r=self.run_mode('--run'); self.assertNotEqual(r.returncode,0); self.assertIn('CRITICAL',r.stdout)
                self.assertTrue((self.root/'home/.renew-critical').exists())
                self.assertEqual((self.root/'home/.renew-critical').stat().st_mode&0o777,0o600)
                self.assertTrue(pending.exists() or pending.is_symlink())
                if scenario!='missing': self.assertTrue(backup.exists())
                self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))
                self.assertEqual((outside/'secret').read_text(),'DO_NOT_LOG')

    def test_pending_escape_is_rejected_before_any_external_file_read(self):
        self.interrupted()
        pending=self.root/'home/.promotion-pending'; data=json.loads(pending.read_text()); data['rollback']=str(self.root/'external'); pending.write_text(json.dumps(data))
        spec=importlib.util.spec_from_file_location('certificate_state',ROOT/'deploy/scripts/cert-state.py'); module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        original=module.regular; reads=[]
        def observed(path,*args):
            reads.append(path); self.assertTrue(path.is_relative_to(self.root/'home')); return original(path,*args)
        with patch.object(module,'regular',side_effect=observed):
            with self.assertRaises(AssertionError): module.State(str(self.root/'home'),str(self.root/'prod')).read_pending()
        self.assertEqual(reads,[pending])

    def test_real_sigkill_after_first_production_rename_is_recoverable(self):
        wrapper=self.root/'kill-state.py'
        wrapper.write_text("import os,runpy,signal\nreal=os.replace\ndef replace(a,b):\n real(a,b)\n if str(b)==os.environ['PRODUCTION_TLS']+'/fullchain.pem':\n  os.kill(os.getppid(),signal.SIGKILL)\n  os.kill(os.getpid(),signal.SIGKILL)\nos.replace=replace\nrunpy.run_path(os.environ['REAL_STATE_TOOL'],run_name='__main__')\n")
        self.env['CERT_STATE_TOOL']=str(wrapper)
        r=self.run_mode('--adopt'); self.assertNotEqual(r.returncode,0)
        pending=self.root/'home/.promotion-pending'; self.assertTrue(pending.exists()); self.assertEqual(pending.stat().st_mode&0o777,0o600)
        self.assertFalse((self.root/'home/.production-adopted').exists())
        self.env['CERT_STATE_TOOL']=self.env['REAL_STATE_TOOL']; (self.root/'calls').write_text('')
        self.assert_recovered()

    def test_noop_health_failure_is_not_reported_as_success(self):
        self.run_mode('--adopt',success=True)
        for scenario in ['oidc','firebot','remote-chain','remote-fingerprint']:
            with self.subTest(gate=scenario):
                r=self.run_mode('--run',scenario)
                self.assertNotEqual(r.returncode,0); self.assertNotIn('CERT_RENEW_RESULT=NOOP',r.stdout)

    def test_orphan_cleanup_validates_ownership_and_preserves_critical_material(self):
        backup=self.interrupted(); (self.root/'home/.promotion-pending').unlink()
        staged_key=self.root/'prod/.promotion-ABC123-key.state-abcdefgh'
        shutil.copyfile(self.root/'old/privkey.pem',staged_key); staged_key.chmod(0o600)
        # This models a committed transaction whose cleanup was interrupted.
        self.run_mode('--run',success=True)
        self.assertFalse(backup.exists()); self.assertFalse(staged_key.exists())
        backup=self.root/'home/deploy/.rollback.ZYX987'; backup.mkdir(mode=0o700)
        (self.root/'home/.renew-critical').touch(mode=0o600)
        r=self.run_mode('--run'); self.assertNotEqual(r.returncode,0); self.assertTrue(backup.exists())

    def test_untrusted_orphan_is_refused_without_following_or_deleting_target(self):
        outside=self.root/'outside'; outside.mkdir(mode=0o700)
        key=outside/'privkey.pem'; key.write_text('DO_NOT_LOG'); key.chmod(0o600)
        backup=self.root/'home/deploy/.rollback.ABC123'; backup.symlink_to(outside,target_is_directory=True)
        r=self.run_mode('--run'); self.assertNotEqual(r.returncode,0); self.assertIn('CRITICAL',r.stdout)
        self.assertTrue(backup.is_symlink()); self.assertEqual(key.read_text(),'DO_NOT_LOG')
        self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))

    def test_seven_day_minimum_accepts_eight_day_candidate(self):
        shutil.copyfile(self.m/'above.pem',self.stage/'fullchain.pem'); shutil.copyfile(self.m/'above.key',self.stage/'privkey.pem')
        self.run_mode('--adopt',success=True)

    def test_failed_critical_write_still_revokes_scheduling(self):
        backup=self.interrupted(adopted=True)
        spec=importlib.util.spec_from_file_location('critical_state',ROOT/'deploy/scripts/cert-state.py'); module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        with patch.object(module,'atomic',side_effect=OSError('fixture full filesystem')):
            with self.assertRaises(OSError): module.State(str(self.root/'home'),str(self.root/'prod')).latch('state-policy')
        self.assertFalse((self.root/'home/.production-adopted').exists())
        self.assertTrue((self.root/'home/.promotion-pending').exists()); self.assertTrue(backup.exists())
        self.assert_recovered()
        self.run_mode('--run',success=True)
        self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))

    def test_dangling_critical_latch_cannot_authorize_ca(self):
        self.adopted(); (self.root/'home/.renew-critical').symlink_to(self.root/'missing-critical')
        r=self.run_mode('--run'); self.assertNotEqual(r.returncode,0); self.assertIn('CRITICAL',r.stdout)
        self.assertFalse(any(c[0]=='acme.sh' for c in self.calls()))

    def test_acme_diagnostics_are_sanitized_private_and_bounded(self):
        self.adopted()
        for reason in ['network','CA','challenge']:
            r=self.run_mode('--run','acme-error-'+reason); self.assertNotEqual(r.returncode,0)
            self.assertIn('CERT_ACME_ERROR='+reason,r.stdout)
        log=self.root/'home/cert-diagnostics.jsonl'; data=log.read_text()
        self.assertNotIn('PRIVATE_KEY_SENTINEL',data); self.assertNotIn('DO_NOT_LOG',data)
        self.assertEqual(log.stat().st_mode&0o777,0o600)
        for _ in range(4):
            log.write_text(' '*32768); self.state('diagnostic','validation')
        self.assertLessEqual(sum(p.stat().st_size for p in (self.root/'home').glob('cert-diagnostics.jsonl*')),3*32768)


@unittest.skipUnless(LINUX_ROOT, 'isolated installer gates require Linux root')
class CertificateProvisionGates(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix='crane-provision-'); self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name); (self.root/'home').mkdir(mode=0o700); (self.root/'bin').mkdir()
        (self.root/'home/.renew.lock').touch(mode=0o600)
        for name, code in {
            'systemctl': "import os,sys\nif sys.argv[-1].endswith('.service'):\n print('active' if os.environ.get('ACTIVE_SERVICE') else 'inactive')\n sys.exit(0 if os.environ.get('ACTIVE_SERVICE') else 3)\nsys.exit(3)\n",
            'git': "import os,pathlib,sys\npathlib.Path(os.environ['PROVISION_FIXTURE'],'git-called').touch()\nsys.exit(1)\n",
            'install': "import os,pathlib,sys\npathlib.Path(os.environ['PROVISION_FIXTURE'],'install-called').touch()\nsys.exit(1)\n",
        }.items():
            path=self.root/'bin'/name; path.write_text('#!/usr/bin/python3\n'+code); path.chmod(0o755)
        self.env={**os.environ,'PATH':str(self.root/'bin')+':'+os.environ['PATH'],'ACME_HOME':str(self.root/'home'),'PROVISION_FIXTURE':str(self.root)}

    def run_install(self, extra=None):
        return subprocess.run(['bash',str(ROOT/'deploy/scripts/provision-acme.sh'),'--install'],env={**self.env,**(extra or {})},capture_output=True,text=True,timeout=10)

    def test_active_service_refuses_before_install_or_upstream(self):
        result=self.run_install({'ACTIVE_SERVICE':'1'})
        self.assertNotEqual(result.returncode,0); self.assertIn('reason=service-active',result.stdout)
        self.assertFalse((self.root/'git-called').exists()); self.assertFalse((self.root/'install-called').exists())

    def test_renewal_lock_refuses_before_overwriting_script_or_unit(self):
        import fcntl
        with (self.root/'home/.renew.lock').open() as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            result=self.run_install()
        self.assertNotEqual(result.returncode,0); self.assertIn('reason=renewal-lock',result.stdout)
        self.assertFalse((self.root/'git-called').exists()); self.assertFalse((self.root/'install-called').exists())

    def test_inactive_service_and_free_lock_can_reach_signature_verification(self):
        result=self.run_install()
        self.assertNotEqual(result.returncode,0)  # fake Git stops, never install unverified bytes
        self.assertTrue((self.root/'git-called').exists()); self.assertFalse((self.root/'install-called').exists())


if __name__ == '__main__': unittest.main()
