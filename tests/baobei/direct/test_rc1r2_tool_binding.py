"""G5-RC1-R2: the takeover-administrator precheck runs the approved tool bytes of
the candidate release, and its record is bound to that release and change.

- `prepare` snapshots direct-deploy.py and admin-preflight.py from the candidate
  commit into the release (tools/) and records their hashes in the manifest.
- `preflight` refuses unless the release manifest is intact for this SOURCE_SHA,
  the snapshotted tools match the manifest, and the running direct-deploy.py is
  byte-identical to the snapshot; it then executes the snapshotted
  admin-preflight.py (never the working tree) and records tool and release
  identity in the sealed record.
- `switch` re-checks all of it, and the --approval must be the change the
  precheck was recorded for (A's precheck cannot complete change B).
The real Keycloak run is tests/baobei/keycloak-admin-preflight.py.
"""
import importlib.util
import json
from pathlib import Path
import shutil
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
ENTRY = ROOT / "deploy/baobei/direct-deploy.py"
SPEC = importlib.util.spec_from_file_location("direct_deploy_rc1r2", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)

NEW = "c" * 40
ADMIN = "11111111-1111-4111-8111-111111111111"
KC_SUB = "33333333-3333-4333-8333-333333333333"
SUBJECT = "44444444-4444-4444-8444-444444444444"
ISSUER = "https://crane.youdaozd.com/realms/baobei"


def tool_result():
    return {"ADMIN_ACCOUNT_VERIFIED": "PASS", "ADMIN_LOGIN_VERIFIED": "PASS", "realm": "baobei", "issuer": ISSUER,
            "admin": {"staffId": ADMIN, "keycloakSubject": KC_SUB, "businessSubject": SUBJECT, "problems": []},
            "login": {"sub": KC_SUB, "roles": ["baobei_admin"]}}


class Release:
    """A prepared baobei release whose tools/ snapshot is the current source."""

    def __init__(self, tmp):
        self.state = Path(tmp).resolve() / "state"
        self.state.mkdir(mode=0o700)
        self.rid = direct.release_id(NEW, "baobei")
        self.release = self.state / "releases" / self.rid
        (self.release / "tools").mkdir(parents=True)
        for name in direct.TOOL_FILES:
            shutil.copyfile(ROOT / "deploy/baobei" / Path(name).name, self.release / name)
        manifest = {"mode": "direct-local", "source_sha": NEW, "source": direct.SOURCE, "identity_realm": "baobei",
                    "identity_issuer": ISSUER, "tools_sha256": direct.hash_files(self.release, direct.TOOL_FILES)}
        direct.atomic_json(self.release / "manifest.json", manifest)
        for phase in direct.PHASES[:direct.PHASES.index("IDENTITY_PRECHECK")]:
            direct.record_phase(self.state, self.rid, phase, "evidence-" + phase.lower())

    def preflight(self, change="CHG-A"):
        with patch.object(direct, "execute_preflight_tool", return_value=tool_result()) as run:
            window = direct.preflight(self.state, self.rid, staff_id=ADMIN, change=change,
                                      tool_args={"base": "https://crane.youdaozd.com"})
        return window, run


class ToolIntegrityTests(unittest.TestCase):
    def test_tools_are_part_of_the_release_snapshot(self):
        self.assertEqual(set(direct.TOOL_FILES), {"tools/direct-deploy.py", "tools/admin-preflight.py"})

    def test_preflight_runs_the_snapshotted_helper_and_records_identity(self):
        with TemporaryDirectory() as tmp:
            r = Release(tmp)
            window, run = r.preflight()
            helper = run.call_args.args[0]
            self.assertEqual(Path(helper), r.release / "tools/admin-preflight.py", "helper not loaded from the release snapshot")
            m = window["phases"][-1]["measured"]
            manifest = json.loads((r.release / "manifest.json").read_text())
            self.assertEqual(m["source_sha"], NEW)
            self.assertEqual(m["tools_sha256"], manifest["tools_sha256"])
            self.assertEqual(m["manifest_sha256"], direct.digest((r.release / "manifest.json").read_bytes()))
            self.assertEqual(m["tool_hashes"]["tools/admin-preflight.py"],
                             direct.digest((r.release / "tools/admin-preflight.py").read_bytes()))

    def test_replaced_helper_is_refused_before_and_after_preflight(self):
        with TemporaryDirectory() as tmp:
            r = Release(tmp)
            helper = r.release / "tools/admin-preflight.py"
            original = helper.read_bytes()
            helper.write_bytes(original + b"\n# replaced\n")
            with self.assertRaisesRegex(ValueError, "工具"):
                r.preflight()
            self.assertFalse((r.state / "preflight.json").exists())
            helper.write_bytes(original)
            r.preflight()
            helper.write_bytes(original + b"\n# replaced after preflight\n")
            with self.assertRaisesRegex(ValueError, "工具"):
                direct.switch_command(r.state, r.rid, ADMIN, "op-1", "切换", "CHG-A")

    def test_missing_helper_is_refused(self):
        with TemporaryDirectory() as tmp:
            r = Release(tmp)
            (r.release / "tools/admin-preflight.py").unlink()
            with self.assertRaisesRegex(ValueError, "工具"):
                r.preflight()

    def test_running_tool_must_equal_the_snapshot(self):
        with TemporaryDirectory() as tmp:
            r = Release(tmp)
            snap = r.release / "tools/direct-deploy.py"
            snap.write_bytes(snap.read_bytes() + b"\n# other version\n")
            direct.atomic_json(r.release / "manifest.json", {**json.loads((r.release / "manifest.json").read_text()),
                                                                 "tools_sha256": direct.hash_files(r.release, direct.TOOL_FILES)})
            with self.assertRaisesRegex(ValueError, "工具"):
                r.preflight()

    def test_other_release_version_is_refused(self):
        with TemporaryDirectory() as tmp:
            r = Release(tmp)
            r.preflight()
            manifest = json.loads((r.release / "manifest.json").read_text())
            direct.atomic_json(r.release / "manifest.json", {**manifest, "source_sha": "d" * 40})
            with self.assertRaisesRegex(ValueError, "发布"):
                direct.switch_command(r.state, r.rid, ADMIN, "op-1", "切换", "CHG-A")
            direct.atomic_json(r.release / "manifest.json", {**manifest, "tools_sha256": "0" * 64})
            with self.assertRaisesRegex(ValueError, "工具|发布"):
                direct.switch_command(r.state, r.rid, ADMIN, "op-1", "切换", "CHG-A")

    def test_release_without_tool_snapshot_is_refused(self):
        with TemporaryDirectory() as tmp:
            r = Release(tmp)
            manifest = json.loads((r.release / "manifest.json").read_text())
            manifest.pop("tools_sha256")
            direct.atomic_json(r.release / "manifest.json", manifest)
            with self.assertRaisesRegex(ValueError, "工具"):
                r.preflight()

    def test_edited_record_is_refused(self):
        with TemporaryDirectory() as tmp:
            r = Release(tmp)
            r.preflight()
            record = json.loads((r.state / "preflight.json").read_text())
            for field, value in (("source_sha", "d" * 40), ("tools_sha256", "0" * 64), ("change", "CHG-B")):
                direct.atomic_json(r.state / "preflight.json", {**record, field: value})
                with self.subTest(field=field), self.assertRaisesRegex(ValueError, "预检"):
                    direct.switch_command(r.state, r.rid, ADMIN, "op-1", "切换", "CHG-A")


class ChangeBindingTests(unittest.TestCase):
    def test_precheck_of_change_a_cannot_complete_change_b(self):
        with TemporaryDirectory() as tmp:
            r = Release(tmp)
            r.preflight(change="CHG-A")
            with self.assertRaisesRegex(ValueError, "变更"):
                direct.switch_command(r.state, r.rid, ADMIN, "op-1", "切换", "CHG-B")
            cmd = direct.switch_command(r.state, r.rid, ADMIN, "op-1", "切换", "CHG-A")
            self.assertEqual(cmd[cmd.index("--approval") + 1], "CHG-A")


if __name__ == "__main__":
    unittest.main()
