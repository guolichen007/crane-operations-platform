"""G5-RC1: the takeover-administrator precheck is evidence the release tool
produced itself, never an editable JSON file.

direct-deploy.py `preflight` runs admin-preflight.py (real Keycloak) as part of
the release tool, binds the result to {release id, realm, issuer, approved
staff id, Keycloak subject, business subject, change reference, generated_at,
expires_at} and records IDENTITY_PRECHECK only from that run. The record is
sealed with a key that exists only inside the private state directory (0600,
created by the tool, never printed); `--preflight-report` (an arbitrary file)
no longer exists. identity-switch is only invoked after the record re-verifies
(same release / realm / issuer / admin, not expired, seal intact).

These tests fake only the Keycloak side (the subprocess result of
admin-preflight.py); the real Keycloak run is tests/baobei/keycloak-admin-preflight.py.
"""
import importlib.util
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
ENTRY = ROOT / "deploy/baobei/direct-deploy.py"
SPEC = importlib.util.spec_from_file_location("direct_deploy_rc1", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)

NEW = "c" * 40
ADMIN = "11111111-1111-4111-8111-111111111111"
OTHER = "22222222-2222-4222-8222-222222222222"
KC_SUB = "33333333-3333-4333-8333-333333333333"
SUBJECT = "44444444-4444-4444-8444-444444444444"
ISSUER = "https://crane.youdaozd.com/realms/baobei"


def good_tool_result(staff=ADMIN, issuer=ISSUER, realm="baobei", kc=KC_SUB, subject=SUBJECT):
    """What admin-preflight.py prints after a real successful run (masked)."""
    return {"ADMIN_ACCOUNT_VERIFIED": "PASS", "ADMIN_LOGIN_VERIFIED": "PASS",
            "realm": realm, "issuer": issuer,
            "admin": {"staffId": staff, "keycloakSubject": kc, "businessSubject": subject, "login": "139****0001",
                      "problems": []},
            "login": {"result": "ok", "subMatchesLink": True, "roles": ["baobei_admin"], "sub": kc}}


class Window:
    def __init__(self, tmp):
        self.state = Path(tmp).resolve() / "state"
        self.state.mkdir(mode=0o700)
        self.rid = direct.release_id(NEW, "baobei")
        # G5-RC1-R2: a prepared release with its tool snapshot.
        release = self.state / "releases" / self.rid
        (release / "tools").mkdir(parents=True)
        import shutil
        for name in direct.TOOL_FILES:
            shutil.copyfile(ROOT / "deploy/baobei" / Path(name).name, release / name)
        direct.atomic_json(release / "manifest.json", {"mode": "direct-local", "source_sha": NEW, "source": direct.SOURCE,
                                                       "identity_realm": "baobei", "identity_issuer": ISSUER,
                                                       "tools_sha256": direct.hash_files(release, direct.TOOL_FILES)})
        for phase in direct.PHASES[:direct.PHASES.index("IDENTITY_PRECHECK")]:
            direct.record_phase(self.state, self.rid, phase, "evidence-" + phase.lower())


def run_preflight(w, result, staff=ADMIN, change="CHG-RC1-001", now=None):
    """Drive the release tool's own preflight with a faked Keycloak result."""
    with patch.object(direct, "execute_preflight_tool", return_value=result):
        return direct.preflight(w.state, w.rid, staff_id=staff, change=change,
                                tool_args={"base": "https://crane.youdaozd.com"}, now=now)


class ProvenanceTests(unittest.TestCase):
    def test_handwritten_pass_json_is_refused(self):
        with TemporaryDirectory() as tmp:
            w = Window(tmp)
            forged = Path(tmp) / "forged.json"
            forged.write_text(json.dumps({"ADMIN_ACCOUNT_VERIFIED": "PASS", "ADMIN_LOGIN_VERIFIED": "PASS",
                                          "admin": {"staffId": "s"}}))
            # The old entry point is gone: nothing reads an arbitrary PASS file.
            self.assertFalse(hasattr(direct, "preflight_report"), "editable PASS JSON is still accepted")
            # Writing a record by hand into the state directory does not verify either.
            record = {"release": w.rid, "realm": "baobei", "issuer": ISSUER, "staff_id": ADMIN,
                      "keycloak_subject": KC_SUB, "business_subject": SUBJECT, "change": "CHG",
                      "generated_at": datetime.now(timezone.utc).isoformat(),
                      "expires_at": (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat(),
                      "ADMIN_ACCOUNT_VERIFIED": "PASS", "ADMIN_LOGIN_VERIFIED": "PASS", "seal": "0" * 64}
            direct.atomic_json(w.state / "preflight.json", record)
            with self.assertRaisesRegex(ValueError, "预检"):
                direct.verify_preflight_record(w.state, w.rid, ADMIN)
            self.assertEqual(direct.read_window(w.state)["phases"][-1]["phase"], "IDENTITY_EPOCH_INIT")

    def test_valid_real_run_records_the_phase(self):
        with TemporaryDirectory() as tmp:
            w = Window(tmp)
            window = run_preflight(w, good_tool_result())
            entry = window["phases"][-1]
            self.assertEqual((entry["phase"], entry["kind"]), ("IDENTITY_PRECHECK", "machine-verified"))
            m = entry["measured"]
            self.assertEqual((m["realm"], m["issuer"], m["staff_id"], m["keycloak_subject"]), ("baobei", ISSUER, ADMIN, KC_SUB))
            self.assertTrue(m["expires_at"] > m["generated_at"])
            direct.verify_preflight_record(w.state, w.rid, ADMIN)
            # Nothing secret in the record.
            text = (w.state / "preflight.json").read_text()
            self.assertNotIn("seal_key", text)

    def test_failed_or_partial_run_records_nothing(self):
        for result in ({**good_tool_result(), "ADMIN_LOGIN_VERIFIED": "NOT_RUN"},
                       {**good_tool_result(), "ADMIN_LOGIN_VERIFIED": "FAIL"},
                       {**good_tool_result(), "ADMIN_ACCOUNT_VERIFIED": "FAIL"}, {}):
            with self.subTest(result=list(result)[:2]), TemporaryDirectory() as tmp:
                w = Window(tmp)
                with self.assertRaisesRegex(ValueError, "预检"):
                    run_preflight(w, result)
                self.assertEqual(direct.read_window(w.state)["phases"][-1]["phase"], "IDENTITY_EPOCH_INIT")
                self.assertFalse((w.state / "preflight.json").exists())

    def test_result_for_another_admin_realm_or_issuer_is_refused(self):
        for result in (good_tool_result(staff=OTHER), good_tool_result(realm="crane"),
                       good_tool_result(issuer="https://crane.youdaozd.com/realms/crane"),
                       good_tool_result(issuer="https://evil.example/realms/baobei")):
            with self.subTest(result=result["admin"]["staffId"] + result["realm"] + result["issuer"]), TemporaryDirectory() as tmp:
                w = Window(tmp)
                with self.assertRaisesRegex(ValueError, "预检"):
                    run_preflight(w, result)
                self.assertFalse((w.state / "preflight.json").exists())

    def test_login_sub_must_match_account_service_subject(self):
        result = good_tool_result()
        result["login"]["sub"] = OTHER
        with TemporaryDirectory() as tmp:
            w = Window(tmp)
            with self.assertRaisesRegex(ValueError, "预检"):
                run_preflight(w, result)

    def test_record_cannot_be_reused_across_admin_release_realm_or_time(self):
        with TemporaryDirectory() as tmp:
            w = Window(tmp)
            run_preflight(w, good_tool_result())
            # another administrator
            with self.assertRaisesRegex(ValueError, "预检"):
                direct.verify_preflight_record(w.state, w.rid, OTHER)
            # another release / realm
            with self.assertRaisesRegex(ValueError, "预检"):
                direct.verify_preflight_record(w.state, direct.release_id("d" * 40, "baobei"), ADMIN)
            with self.assertRaisesRegex(ValueError, "预检"):
                direct.verify_preflight_record(w.state, NEW, ADMIN)
            # expired
            later = datetime.now(timezone.utc) + direct.PREFLIGHT_VALIDITY + timedelta(seconds=1)
            with self.assertRaisesRegex(ValueError, "过期"):
                direct.verify_preflight_record(w.state, w.rid, ADMIN, now=later)
            # edited field (seal)
            record = json.loads((w.state / "preflight.json").read_text())
            for field, value in (("staff_id", OTHER), ("issuer", "https://crane.youdaozd.com/realms/crane"),
                                 ("expires_at", "2999-01-01T00:00:00+00:00"), ("keycloak_subject", OTHER)):
                direct.atomic_json(w.state / "preflight.json", {**record, field: value})
                with self.subTest(field=field), self.assertRaisesRegex(ValueError, "预检"):
                    direct.verify_preflight_record(w.state, w.rid, record["staff_id"] if field != "staff_id" else OTHER)
            # copied into another state directory (another release host): no key there
            direct.atomic_json(w.state / "preflight.json", record)
            with TemporaryDirectory() as other:
                w2 = Window(other)
                direct.atomic_json(w2.state / "preflight.json", record)
                with self.assertRaisesRegex(ValueError, "预检"):
                    direct.verify_preflight_record(w2.state, w2.rid, ADMIN)

    def test_identity_switch_requires_a_valid_record(self):
        with TemporaryDirectory() as tmp:
            w = Window(tmp)
            with self.assertRaisesRegex(ValueError, "预检"):
                direct.switch_command(w.state, w.rid, ADMIN, operator="op-1", reason="切换", approval="CHG-RC1-001")
            run_preflight(w, good_tool_result())
            cmd = direct.switch_command(w.state, w.rid, ADMIN, operator="op-1", reason="切换", approval="CHG-RC1-001")
            self.assertEqual(cmd[:3], ["maintenance", "identity-switch", "--realm"])
            self.assertIn("baobei", cmd)
            self.assertEqual(cmd[cmd.index("--takeover-admin") + 1], ADMIN)
            with self.assertRaisesRegex(ValueError, "预检"):
                direct.switch_command(w.state, w.rid, OTHER, operator="op-1", reason="切换", approval="CHG-RC1-001")

    def test_precheck_cannot_be_recorded_by_hand(self):
        # Dedicated realm: IDENTITY_PRECHECK and IDENTITY_SWITCH only through the tool.
        for phase in ("IDENTITY_PRECHECK", "IDENTITY_SWITCH"):
            with self.assertRaisesRegex(ValueError, "部署工具"):
                direct.operator_phase_allowed(phase, "baobei")
        direct.operator_phase_allowed("MAINTENANCE_ENTER", "baobei")
        direct.operator_phase_allowed("IDENTITY_PRECHECK", "crane")   # shared realm: no takeover admin


class AdminPreflightSubjectTests(unittest.TestCase):
    def test_missing_baobei_subject_attribute_is_a_problem(self):
        source = (ROOT / "deploy/baobei/admin-preflight.py").read_text(encoding="utf-8")
        self.assertNotIn("not in (None, [admin.get('subject')])", source,
                         "admin-preflight still accepts a Keycloak user without baobei_subject")


if __name__ == "__main__":
    unittest.main()
