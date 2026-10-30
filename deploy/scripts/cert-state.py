#!/usr/bin/env python3
"""Root-only durable certificate state; no PEM or upstream output is logged."""
import datetime as dt
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

ROLLBACK = re.compile(r'\.rollback\.[A-Za-z0-9]{6,32}')
FINGERPRINT = re.compile(r'[0-9A-F]{64}')
REASONS = {'network', 'CA', 'challenge', 'state-policy', 'validation'}


def sync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try: os.fsync(fd)
    finally: os.close(fd)


def directory(path, private=True):
    info = path.lstat()
    assert stat.S_ISDIR(info.st_mode) and info.st_uid == info.st_gid == 0
    assert path.resolve() == path and (info.st_mode & 0o777 == 0o700 if private else not info.st_mode & 0o022)


def regular(path, modes=(0o600,)):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    info = os.fstat(fd)
    if not (stat.S_ISREG(info.st_mode) and info.st_uid == info.st_gid == 0 and stat.S_IMODE(info.st_mode) in modes):
        os.close(fd); raise ValueError('unsafe private file')
    return fd


def private_read(path):
    fd = regular(path)
    with os.fdopen(fd, 'rb') as stream:
        data = stream.read(1024 * 1024 + 1)
    assert len(data) <= 1024 * 1024
    return data


def atomic(path, data, mode=0o600):
    if os.path.lexists(path):
        fd = regular(path, (mode,)); os.close(fd)
    prefix = path.name + '.state-' if path.name.startswith('.promotion-') else '.state-'
    fd, name = tempfile.mkstemp(prefix=prefix, dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            os.fchmod(stream.fileno(), mode)
            stream.write(data); stream.flush(); os.fsync(stream.fileno())
        os.replace(name, path)
        sync_dir(path.parent)
    finally:
        if os.path.exists(name): os.unlink(name)


class State:
    def __init__(self, home, production):
        self.home, self.production = Path(home), Path(production)
        directory(self.home)
        self.deploy = self.home / 'deploy'
        self.pending = self.home / '.promotion-pending'
        self.critical = self.home / '.renew-critical'
        self.marker = self.home / '.production-adopted'

    def backup(self, name, complete=True):
        assert ROLLBACK.fullmatch(name)
        directory(self.deploy)
        path = self.deploy / name
        # Validate basename, canonical parent, ownership and entries before any read/delete.
        directory(path)
        assert path.resolve().parent == self.deploy.resolve()
        names = {p.name for p in path.iterdir()}
        assert names <= {'fullchain.pem', 'privkey.pem'}
        if complete: assert names == {'fullchain.pem', 'privkey.pem'}
        for name in names:
            fd = regular(path / name); os.close(fd)
        return path

    def read_pending(self):
        data = json.loads(private_read(self.pending))
        assert set(data) == {'schema', 'rollback', 'old_fingerprint', 'candidate_fingerprint', 'mode', 'old_adopted'}
        assert type(data['schema']) is int and data['schema'] == 1
        assert data['mode'] in ('--adopt', '--run') and type(data['old_adopted']) is bool
        assert FINGERPRINT.fullmatch(data['old_fingerprint']) and FINGERPRINT.fullmatch(data['candidate_fingerprint'])
        self.backup(data['rollback'])
        return data

    def create_pending(self, name, old, candidate, mode, adopted):
        assert not os.path.lexists(self.pending)
        assert FINGERPRINT.fullmatch(old) and FINGERPRINT.fullmatch(candidate)
        assert mode in ('--adopt', '--run') and adopted in ('0', '1')
        backup = self.backup(name)
        for file in backup.iterdir():
            fd = regular(file)
            try: os.fsync(fd)
            finally: os.close(fd)
        sync_dir(backup); sync_dir(self.deploy)
        data = dict(schema=1, rollback=name, old_fingerprint=old, candidate_fingerprint=candidate, mode=mode, old_adopted=adopted == '1')
        atomic(self.pending, (json.dumps(data) + '\n').encode())

    def remove_marker(self):
        if os.path.lexists(self.marker):
            fd = regular(self.marker); os.close(fd)
            self.marker.unlink(); sync_dir(self.home)

    def temps(self, name):
        assert ROLLBACK.fullmatch(name)
        directory(self.production, private=False)
        return [self.production / ('.promotion-' + name[10:] + '-' + kind) for kind in ('cert', 'key')]

    def clean_temps(self, name):
        for path in self.temps(name):
            # Include a temp write interrupted before its atomic rename. Its prefix
            # is bound to this validated rollback basename, never a broad TLS glob.
            candidates = [path, *self.production.glob(path.name + '.state-*')]
            for candidate in candidates:
                if os.path.lexists(candidate):
                    fd = regular(candidate, (0o600, 0o644)); os.close(fd)
                    candidate.unlink()
        sync_dir(self.production)

    def install_pair(self, source):
        data = self.read_pending()
        source = Path(source)
        assert source in (self.backup(data['rollback']), self.deploy / 'crane.youdaozd.com')
        directory(source)
        directory(self.production, private=False)
        cert = source / 'fullchain.pem'; key = source / 'privkey.pem'
        fd = regular(cert, (0o600, 0o644))
        with os.fdopen(fd, 'rb') as stream: cert_bytes = stream.read(1024 * 1024 + 1)
        key_bytes = private_read(key)
        assert len(cert_bytes) <= 1024 * 1024
        temp_cert, temp_key = self.temps(data['rollback'])
        atomic(temp_cert, cert_bytes, 0o644); atomic(temp_key, key_bytes)
        for temp, target in [(temp_cert, self.production / 'fullchain.pem'), (temp_key, self.production / 'privkey.pem')]:
            if os.path.lexists(target):
                fd = regular(target, (0o600, 0o644)); os.close(fd)
            os.replace(temp, target); sync_dir(self.production)

    def delete_backup(self, name):
        path = self.backup(name, complete=False)
        self.clean_temps(name)
        for file in path.iterdir(): file.unlink()
        path.rmdir(); sync_dir(self.deploy)

    def finish(self, recovery=False):
        data = self.read_pending()
        if recovery and not data['old_adopted']: self.remove_marker()
        # A crash after this unlink leaves only a verified orphan, cleaned under the lock.
        self.pending.unlink(); sync_dir(self.home)
        self.delete_backup(data['rollback'])

    def gc(self):
        if os.path.lexists(self.pending) or os.path.lexists(self.critical): return
        if not self.deploy.exists(): return
        directory(self.deploy)
        for path in self.deploy.glob('.rollback.*'): self.delete_backup(path.name)

    def latch(self, reason):
        assert reason in REASONS
        # Revoke scheduling first, including when a full filesystem prevents the
        # durable critical write. Pending recovery material remains untouched.
        self.remove_marker()
        data = {'schema': 1, 'reason': reason, 'pending': '.promotion-pending' if os.path.lexists(self.pending) else None}
        atomic(self.critical, (json.dumps(data) + '\n').encode())

    def adopt(self, fingerprint):
        assert FINGERPRINT.fullmatch(fingerprint) and not os.path.lexists(self.marker)
        atomic(self.marker, (json.dumps({'schema': 1, 'fingerprint': fingerprint}) + '\n').encode())

    def diagnostic(self, reason, code=1, operation='lifecycle'):
        assert reason in REASONS and operation in ('lifecycle', 'issue', 'install-cert', 'cron')
        files = [self.home / ('cert-diagnostics.jsonl' + suffix) for suffix in ('', '.1', '.2')]
        for file in files:
            if os.path.lexists(file):
                fd = regular(file); os.close(fd)
        entry = (json.dumps({'at': dt.datetime.now(dt.timezone.utc).isoformat(), 'reason': reason, 'code': int(code), 'operation': operation}) + '\n').encode()
        if files[0].exists() and files[0].stat().st_size + len(entry) > 32768:
            if files[2].exists(): files[2].unlink()
            if files[1].exists(): os.replace(files[1], files[2])
            os.replace(files[0], files[1]); sync_dir(self.home)
        fd = os.open(files[0], os.O_WRONLY | os.O_CREAT | os.O_APPEND | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'ab') as stream: stream.write(entry); stream.flush(); os.fsync(stream.fileno())
        sync_dir(self.home)

    def acme(self, executable, args):
        # Drain all upstream output but retain at most 64KiB in RAM, never raw on disk/journal.
        p = subprocess.Popen([executable, *args], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        tail = b''
        while True:
            chunk = p.stdout.read(8192)
            if not chunk: break
            tail = (tail + chunk)[-65536:]
        code = p.wait()
        if code:
            text = tail.decode(errors='replace').lower()
            reason = 'challenge' if re.search(r'challenge|unauthorized|invalid response', text) else 'network' if re.search(r'timeout|timed out|connect|resolve|network|handshake', text) else 'CA'
            operation = 'cron' if '--cron' in args else 'install-cert' if '--install-cert' in args else 'issue'
            self.diagnostic(reason, code, operation)
            print('CERT_ACME_ERROR=' + reason)
        return 0 if code == 0 else 1


def main():
    assert os.geteuid() == 0
    home, production, operation, *args = sys.argv[1:]
    state = State(home, production)
    if operation == 'create': state.create_pending(*args)
    elif operation == 'read':
        data = state.read_pending()
        print('\t'.join([data['rollback'], data['old_fingerprint'], data['candidate_fingerprint'], data['mode'], '1' if data['old_adopted'] else '0']))
    elif operation == 'install': state.install_pair(*args)
    elif operation == 'finish': state.finish(recovery=args == ['recovery'])
    elif operation == 'gc': state.gc()
    elif operation == 'critical': state.latch(*args)
    elif operation == 'adopt': state.adopt(*args)
    elif operation == 'diagnostic': state.diagnostic(*args)
    elif operation == 'acme': return state.acme(args[0], args[1:])
    else: raise ValueError('invalid state operation')
    return 0


if __name__ == '__main__':
    try: sys.exit(main())
    except Exception:
        # Deliberately exclude paths, JSON contents, PEM and raw exception strings.
        print('CERT_STATE_ERROR=state-policy', file=sys.stderr)
        sys.exit(1)
