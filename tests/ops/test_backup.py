import importlib.util
import json
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('crane_backup', Path(__file__).parents[2] / 'deploy/scripts/backup.py')
backup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)


class BackupSafetyTests(unittest.TestCase):
    def test_complete_set_is_atomic_and_checksums_match(self):
        with tempfile.TemporaryDirectory() as directory:
            def dump(args, database, query, destination):
                destination.write_bytes(b'anonymous-test-dump')
                return {'count': 1}
            args = types.SimpleNamespace(root=Path(directory), source_sha='a'*40, retention_days=14)
            with patch.object(backup, 'snapshot_dump', side_effect=dump):
                backup.backup(args)
            completed = [path for path in Path(directory).iterdir() if backup.NAME.fullmatch(path.name)]
            self.assertEqual(len(completed), 1)
            metadata = json.loads((completed[0] / 'metadata.json').read_text())
            self.assertEqual(set(metadata['databases']), {'application', 'keycloak'})
            self.assertEqual(backup.digest(completed[0] / 'metadata.json'), (completed[0] / 'metadata.sha256').read_text().strip())

    def test_incomplete_backup_never_becomes_completed_set(self):
        with tempfile.TemporaryDirectory() as directory:
            args = types.SimpleNamespace(root=Path(directory), source_sha='a'*40, retention_days=14)
            with patch.object(backup, 'snapshot_dump', side_effect=RuntimeError('simulated disk failure')):
                with self.assertRaises(RuntimeError):
                    backup.backup(args)
            self.assertFalse(any(backup.NAME.fullmatch(path.name) for path in Path(directory).iterdir()))

    def test_checksum_failure_precedes_any_database_operation(self):
        with tempfile.TemporaryDirectory() as directory:
            completed = Path(directory) / 'crane-backup-20261028T000000Z-1234abcd'
            completed.mkdir()
            (completed / 'metadata.json').write_text(json.dumps({'schema_version': 1}))
            (completed / 'metadata.sha256').write_text('0'*64)
            with patch.object(backup, 'sql') as sql:
                with self.assertRaises(RuntimeError):
                    backup.drill(types.SimpleNamespace(backup=completed))
                sql.assert_not_called()

    def test_root_and_non_completed_targets_are_rejected(self):
        with self.assertRaises(RuntimeError):
            backup.guard_directory(Path(Path.cwd().anchor))
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(RuntimeError):
                backup.drill(types.SimpleNamespace(backup=Path(directory)))


if __name__ == '__main__':
    unittest.main()
