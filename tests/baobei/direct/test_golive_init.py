"""V1.1 go-live initialization tool (deploy/baobei/golive-init.py).

Unit checks run everywhere. The isolated drill (a disposable PostgreSQL with
the production roles, every migration and synthetic business data; build,
swap, verify and rollback) runs when BAOBEI_GOLIVE_DRILL_DSN_PORT names a
loopback cluster whose superuser is baobei_bootstrap (CI db job / local).
"""
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
SPEC = importlib.util.spec_from_file_location("golive_init", ROOT / "deploy/baobei/golive-init.py")
g = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(g)

ADMIN = "aaaaaaaa-0000-4000-8000-000000000001"


def whitelist(tmp, **change):
    body = {"approval": "OWNER-GOLIVE-TEST", "team": {"name": "销售一组"},
            "keep": [{"staff_id": ADMIN, "role": "baobei_admin"}],
            "create": [{"name": "备用管理员", "role": "baobei_admin", "claim_login": "admin02"}] +
                      [{"name": f"销售0{i}", "role": "baobei_sales", "claim_login": f"sales0{i}"} for i in range(1, 6)]}
    body.update(change)
    path = Path(tmp) / "wl.json"
    path.write_text(json.dumps(body, ensure_ascii=False))
    path.chmod(0o600)
    return path


class WhitelistTests(unittest.TestCase):
    def test_owner_target_is_valid(self):
        with tempfile.TemporaryDirectory() as tmp:
            wl = g.load_whitelist(whitelist(tmp))
            self.assertEqual(wl["team"], "销售一组")
            self.assertEqual(len(wl["create"]), 6)
            self.assertEqual(sum(c["role"] == "baobei_sales" for c in wl["create"]), 5)

    def test_invalid_whitelists_are_refused(self):
        bad = [
            {"approval": ""},
            {"team": {"name": ""}},
            {"team": {"name": "A", "id": "x"}},
            {"keep": []},
            {"keep": [{"staff_id": "not-a-uuid", "role": "baobei_admin"}]},
            {"keep": [{"staff_id": ADMIN, "role": "baobei_admin"}, {"staff_id": ADMIN, "role": "baobei_admin"}]},
            {"create": [{"name": "x", "role": "baobei_sales", "claim_login": "13800000000"}]},
            {"create": [{"name": "x", "role": "root", "claim_login": "sales01"}]},
            {"create": [{"name": "12345", "role": "baobei_sales", "claim_login": "sales01"}]},
            {"create": [{"name": "a", "role": "baobei_sales", "claim_login": "s01"}, {"name": "b", "role": "baobei_sales", "claim_login": "s01"}]},
            {"create": [{"name": "a", "role": "baobei_sales", "claim_login": "sales01", "phone": "+8613800000000"}]},
            {"extra": 1},
        ]
        for change in bad:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as tmp:
                with self.assertRaises(g.Refused):
                    g.load_whitelist(whitelist(tmp, **change))

    def test_whitelist_must_be_private(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = whitelist(tmp)
            path.chmod(0o644)
            with self.assertRaises(g.Refused):
                g.load_whitelist(path)


class ClassificationTests(unittest.TestCase):
    def test_every_table_of_the_schema_has_one_rule(self):
        created = set()
        for f in sorted((ROOT / "services/project-intake-api/migrations").glob("*.sql")):
            created |= set(re.findall(r"CREATE TABLE (?:IF NOT EXISTS )?(\w+)", f.read_text(encoding="utf-8")))
        created |= {"baobei_migrations", "baobei_environment"}
        rules = set(g.KEEP_ALL) | set(g.FILTERED) | set(g.EMPTY)
        self.assertEqual(len(rules), len(g.KEEP_ALL) + len(g.FILTERED) + len(g.EMPTY), "a table has two rules")
        self.assertEqual(sorted(created - rules), [], "new table without an initialization rule")
        self.assertEqual(sorted(rules - created), [], "rule for a table that does not exist")

    def test_business_evidence_is_never_kept(self):
        for table in ("intake_projects", "approvals", "project_submissions", "contacts", "project_activities",
                      "project_technical_briefs", "customer_audit", "account_operations", "app_sessions"):
            self.assertIn(table, g.EMPTY)

    def test_no_trigger_is_disabled_and_no_live_row_deleted(self):
        source = (ROOT / "deploy/baobei/golive-init.py").read_text(encoding="utf-8")
        for forbidden in ("DISABLE TRIGGER", "session_replication_role", "TRUNCATE", "DELETE FROM", "DROP TABLE"):
            self.assertNotIn(forbidden, source, forbidden)
        # Only scratch databases created by this tool are ever dropped.
        for drop in re.findall(r"DROP DATABASE IF EXISTS \{(\w+)\}", source):
            self.assertIn(drop, ("leftover", "scratch", "check"))

    def test_no_network_interface(self):
        source = (ROOT / "deploy/baobei/golive-init.py").read_text(encoding="utf-8")
        for forbidden in ("import http", "socketserver", "import flask", "urllib.request", "import requests", "import socket"):
            self.assertNotIn(forbidden, source, forbidden)

    def test_local_mode_is_drill_only(self):
        os.environ.pop("BAOBEI_GOLIVE_DRILL", None)
        with self.assertRaises(g.Refused):
            g.Psql(None, ("127.0.0.1", 5432))
        os.environ["BAOBEI_GOLIVE_DRILL"] = "1"
        try:
            with self.assertRaises(g.Refused):
                g.Psql(None, ("10.0.0.5", 5432))
        finally:
            os.environ.pop("BAOBEI_GOLIVE_DRILL")


@unittest.skipUnless(os.environ.get("BAOBEI_GOLIVE_DRILL_PORT"), "NOT_RUN: disposable PostgreSQL drill cluster required")
class IsolatedDrillTests(unittest.TestCase):
    """End to end on a disposable cluster prepared by tests/baobei/golive-drill.sh."""

    def test_inventory_backup_build_swap_verify_rollback(self):
        port = os.environ["BAOBEI_GOLIVE_DRILL_PORT"]
        state = Path(tempfile.mkdtemp(prefix="golive-state-"))
        state.chmod(0o700)
        with tempfile.TemporaryDirectory() as tmp:
            wl = whitelist(tmp)
            base = ["python3", "-B", str(ROOT / "deploy/baobei/golive-init.py"), "--state-dir", str(state),
                    "--whitelist", str(wl), "--local", "127.0.0.1:" + port]
            env = {**os.environ, "BAOBEI_GOLIVE_DRILL": "1"}

            def step(name, ok=True):
                args = base + ([name] if name == "inventory" else ["--approval", "OWNER-GOLIVE-TEST", "--operator", "drill", name])
                r = subprocess.run(args, capture_output=True, env=env)
                out = json.loads(r.stdout or b"{}")
                if ok:
                    self.assertEqual(r.returncode, 0, out)
                return out
            self.assertTrue(step("inventory")["DRY_RUN"])
            self.assertEqual(step("swap", ok=False).get("REFUSED", "")[:8], "没有与本白名单对应"[:8])
            self.assertEqual(step("backup")["restore_check"], "PASS")
            built = step("build")
            self.assertEqual(built["schema"], "identical")
            self.assertEqual(built["counts"]["intake_projects"], 0)
            self.assertEqual(built["counts"]["teams"], 1)
            self.assertEqual(built["counts"]["app_users"], 7)
            self.assertEqual(step("swap")["SWAP"], "PASS")
            self.assertEqual(step("verify")["VERIFY"], "PASS")
            self.assertEqual(step("rollback")["ROLLBACK"], "PASS")
