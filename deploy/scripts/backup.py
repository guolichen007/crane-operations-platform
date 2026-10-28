#!/usr/bin/env python3
"""Atomic dual-database backup and isolated restore drill; no secrets in command lines or output."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import time
import uuid

APP_COUNTS = "SELECT json_build_object('projects',(SELECT count(*) FROM projects),'assets',(SELECT count(*) FROM asset_devices),'users',(SELECT count(*) FROM users),'memberships',(SELECT count(*) FROM project_memberships),'sessions',(SELECT count(*) FROM web_sessions),'audits',(SELECT count(*) FROM audit_logs),'schema_version',(SELECT max(version_id) FROM goose_db_version WHERE is_applied),'session_columns',(SELECT json_agg(column_name ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name='web_sessions'),'audit_columns',(SELECT json_agg(column_name ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name='audit_logs'));"
KC_COUNTS = "SELECT json_build_object('realms',(SELECT count(*) FROM realm),'users',(SELECT count(*) FROM user_entity),'clients',(SELECT count(*) FROM client));"
DATABASES = {'application': ('crane_app', APP_COUNTS), 'keycloak': ('crane_keycloak', KC_COUNTS)}
NAME = re.compile(r'^crane-backup-\d{8}T\d{6}Z-[0-9a-f]{8}$')


def digest(path):
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(chunk)
    return h.hexdigest()


def sync_directory(path):
    if os.name != 'nt':
        descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(descriptor)
        finally:
            os.close(descriptor)


def command(args):
    return [args.docker, 'compose', '--env-file', str(args.env_file), '-f', str(args.compose), 'exec', '-T', '--user', 'postgres', 'postgres']


def checked(command_args, **kwargs):
    result = subprocess.run(command_args, stderr=subprocess.PIPE, **kwargs)
    if result.returncode:
        raise RuntimeError('database operation failed; upstream output redacted')
    return result


def sql(args, database, statement):
    return checked(command(args) + ['psql', '-XqAt', '-v', 'ON_ERROR_STOP=1', '-d', database], input=statement.encode(), stdout=subprocess.PIPE).stdout.decode().strip()


def guard_directory(path, create=False):
    path = path.absolute()
    if path == Path(path.anchor) or path.is_symlink() or any(p.is_symlink() for p in path.parents):
        raise RuntimeError('unsafe backup directory')
    if create:
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
    if not path.is_dir():
        raise RuntimeError('backup directory missing')
    if os.name != 'nt':
        path.chmod(0o700)
    return path


def snapshot_dump(args, database, query, destination):
    # Keep an exported REPEATABLE READ snapshot alive. Counts and pg_dump see exactly the same rows.
    owner = subprocess.Popen(command(args) + ['psql', '-XqAt', '-v', 'ON_ERROR_STOP=1', '-d', database], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    try:
        owner.stdin.write('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSELECT pg_export_snapshot();\n' + query + '\n')
        owner.stdin.flush()
        snapshot = owner.stdout.readline().strip()
        if not re.fullmatch(r'[0-9A-Fa-f]+-[0-9A-Fa-f]+-[0-9]+', snapshot):
            raise RuntimeError('snapshot unavailable')
        counts = json.loads(owner.stdout.readline())
        with destination.open('xb') as output:
            checked(command(args) + ['pg_dump', '-Fc', '--no-owner', '--no-acl', '--snapshot=' + snapshot, '-d', database], stdout=output)
            output.flush()
            os.fsync(output.fileno())
        owner.stdin.write('COMMIT;\n')
        owner.stdin.close()
        if owner.wait(timeout=30):
            raise RuntimeError('snapshot transaction failed')
        return counts
    finally:
        if owner.poll() is None:
            owner.kill()
        owner.wait()


def backup(args):
    root = guard_directory(args.root, True)
    marker = root / '.crane-backup-root'
    if not marker.exists():
        marker.write_text('crane-backup-v1\n', encoding='ascii')
    if marker.read_text(encoding='ascii') != 'crane-backup-v1\n':
        raise RuntimeError('backup root marker mismatch')
    if not re.fullmatch('[0-9a-f]{40}', args.source_sha):
        raise RuntimeError('full source SHA required')
    now = dt.datetime.now(dt.timezone.utc)
    name = 'crane-backup-' + now.strftime('%Y%m%dT%H%M%SZ') + '-' + uuid.uuid4().hex[:8]
    partial = root / ('.partial-' + name)
    partial.mkdir(mode=0o700)
    metadata = {'schema_version': 1, 'timestamp': now.isoformat(), 'source_sha': args.source_sha, 'databases': {}}
    try:
        for label, (database, query) in DATABASES.items():
            path = partial / (label + '.dump')
            counts = snapshot_dump(args, database, query, path)
            metadata['databases'][label] = {'file': path.name, 'sha256': digest(path), 'snapshot': counts}
        (partial / 'metadata.json').write_text(json.dumps(metadata, indent=2) + '\n', encoding='utf-8')
        (partial / 'metadata.sha256').write_text(digest(partial / 'metadata.json') + '\n', encoding='ascii')
        for path in partial.iterdir():
            if os.name != 'nt':
                path.chmod(0o600)
            # FlushFileBuffers requires a writable handle on Windows.
            with path.open('r+b') as stream:
                os.fsync(stream.fileno())
        sync_directory(partial)
        partial.rename(root / name)  # Same filesystem: incomplete sets never become visible as complete backups.
        sync_directory(root)
    except Exception:
        # Preserve failed partial output privately for local diagnosis; never include it in retention or artifacts.
        raise
    cutoff = now.timestamp() - args.retention_days * 86400
    for path in root.iterdir():
        if NAME.fullmatch(path.name) and path.is_dir() and not path.is_symlink() and path.stat().st_mtime < cutoff:
            if path.resolve().parent != root.resolve():
                raise RuntimeError('retention boundary rejected')
            shutil.rmtree(path)
    print('BACKUP=PASS name=' + name)


def drill(args):
    started = time.monotonic()
    root = guard_directory(args.backup)
    if not NAME.fullmatch(root.name):
        raise RuntimeError('only completed Crane backup sets can be restored')
    if digest(root / 'metadata.json') != (root / 'metadata.sha256').read_text().strip():
        raise RuntimeError('metadata checksum mismatch')
    metadata = json.loads((root / 'metadata.json').read_text(encoding='utf-8'))
    if metadata.get('schema_version') != 1 or set(metadata.get('databases', {})) != set(DATABASES):
        raise RuntimeError('backup metadata schema mismatch')
    for label, item in metadata['databases'].items():
        if item['file'] != label + '.dump' or (root / item['file']).is_symlink() or digest(root / item['file']) != item['sha256']:
            raise RuntimeError('dump checksum or boundary mismatch')
    created = []
    try:
        for label, (_, query) in DATABASES.items():
            target = 'crane_drill_' + uuid.uuid4().hex
            # Never accept a caller-selected restore target. Formal databases cannot be overwritten by this tool.
            sql(args, 'postgres', 'CREATE DATABASE ' + target + ';')
            created.append(target)
            with (root / metadata['databases'][label]['file']).open('rb') as stream:
                checked(command(args) + ['pg_restore', '--exit-on-error', '--no-owner', '--no-acl', '-d', target], stdin=stream, stdout=subprocess.DEVNULL)
            restored = json.loads(sql(args, target, query))
            if restored != metadata['databases'][label]['snapshot']:
                raise RuntimeError('restored counts/schema differ from exported snapshot')
    finally:
        for target in created:
            if not re.fullmatch('crane_drill_[0-9a-f]{32}', target):
                raise RuntimeError('drill cleanup target rejected')
            sql(args, 'postgres', 'DROP DATABASE ' + target + ' WITH (FORCE);')
    elapsed = round(time.monotonic() - started, 3)
    if elapsed > 4 * 3600:
        raise RuntimeError('restore drill exceeded RTO target')
    print('RESTORE=PASS isolated_databases=2 elapsed_seconds=' + str(elapsed))


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('operation', choices=['backup', 'drill'])
    parser.add_argument('--compose', type=Path, default=Path('deploy/compose/docker-compose.prod.yml'))
    parser.add_argument('--env-file', type=Path, default=Path('deploy/compose/production.env'))
    parser.add_argument('--docker', default='docker')
    parser.add_argument('--root', type=Path, default=Path('/srv/u-dao/backups/crane-ops'))
    parser.add_argument('--backup', type=Path)
    parser.add_argument('--source-sha', default='')
    parser.add_argument('--retention-days', type=int, default=14)
    args = parser.parse_args()
    if args.retention_days < 1 or args.operation == 'drill' and args.backup is None:
        parser.error('positive retention / explicit completed backup required')
    try:
        backup(args) if args.operation == 'backup' else drill(args)
    except Exception:
        print('Backup/restore rejected; inspect configuration locally. No database output printed.', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
