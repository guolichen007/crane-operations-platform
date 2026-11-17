"""CR-08 release-state checks for direct-deploy.py (no Docker, no production).

CR-08A  maintenance windows have a kind (upgrade / realm-switch /
        disaster-recovery) with their own phase lists; a same-commit realm
        switch completes without inventing SCHEMA_UPGRADE; interrupted windows
        are aborted explicitly or resumed; old windows stay valid.
CR-08B  path B: verified pre-upgrade backup -> restore into a fresh database ->
        ledger, privileges, identity state and counts measured -> swap -> old
        release started and checked -> only then active/pending move.
CR-08C  each realm uses its own Web client secret file; the dedicated realm
        fails closed without it and refuses a copy of the shared secret; the
        precheck and the release use the same secret.

The tool runs for real against a temporary state directory (real prepare,
verify_release, apply, record_phase, recover); only git, image builds and
Docker/PostgreSQL are simulated by the World of test_rc0r1_lifecycle.
"""
import json
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

import test_rc0r1_lifecycle as lc

direct = lc.direct
OLD, NEW = lc.OLD, lc.NEW
CRANE = direct.REALMS["crane"]["issuer"]
BAOBEI = direct.REALMS["baobei"]["issuer"]


class Host:
    """Production today (old release active) upgraded to NEW on the dedicated realm."""

    def __init__(self, tmp, upgrade=True):
        self.tmp = Path(tmp).resolve()
        self.world = lc.World(self.tmp)
        self.state, self.secrets = self.tmp / "state", self.tmp / "secrets"
        self.state.mkdir(mode=0o700)
        self.secrets.mkdir()
        lc.write_secrets(self.secrets)
        self.docker = lc.FakeDocker(self.world)
        self.world.on_write = self.on_write
        self.world.on_restore = self.on_restore
        self.restore_ledger = None          # what pg_restore produces (default: the backup's)
        self.restore_privileges = lc.minimal_privileges()
        self.restore_counts = None
        self.stack = lc.patched(self.world, self.tmp)
        self.stack.__enter__()
        w = self.world
        w.epoch_issuer = CRANE
        lc.prepare_old(self.docker, self.state, self.secrets)
        self.backup_counts = dict(w.counts)
        self.backup = None
        direct.prepare(self.docker, self.state, self.secrets, NEW, "crane")
        direct.prepare(self.docker, self.state, self.secrets, NEW, "baobei")
        self.crane_id, self.baobei_id = NEW, direct.release_id(NEW, "baobei")
        if upgrade:
            self.upgrade()

    def close(self):
        self.stack.__exit__(None, None, None)

    def make_backup(self, ledger, counts, name="pre-upgrade.dump", created_at=None):
        archive = self.tmp / "backups" / name
        archive.parent.mkdir(exist_ok=True)
        archive.write_bytes(b"PGDMP synthetic archive " + name.encode())
        meta = {"format": "baobei-backup-v1", "database": "baobei", "environment": "production",
                "sha256": direct.digest(archive.read_bytes()), "migrations": ledger, "counts": counts,
                "created_at": created_at or direct.datetime.now(direct.timezone.utc).isoformat()}
        Path(str(archive) + ".json").write_text(json.dumps(meta))
        return archive

    def upgrade(self):
        """The upgrade window up to a running NEW+baobei (as in the G5-RC0 runbook)."""
        s, w, rid = self.state, self.world, self.baobei_id
        direct.record_phase(s, rid, "MAINTENANCE_ENTER", "CHG-UP-1")
        # The runbook's step 2: backup.mjs after writes stopped; its checksum is the evidence.
        self.backup = self.make_backup(w.ledger, w.counts)
        sha = direct.digest(self.backup.read_bytes())
        direct.record_phase(s, rid, "FRESH_BACKUP_VERIFIED", "backup:pre-upgrade.dump:sha256-" + sha[:16])
        direct.record_phase(s, rid, "OLD_API_DRAINED", "drain-1")
        w.api_state, w.connections = "exited", 0
        direct.apply(self.docker, s, self.secrets, rid, "migrate", self.secrets / "db_password")
        w.epoch_issuer = None
        direct.record_phase(s, rid, "IDENTITY_EPOCH_INIT", "epoch:1:crane")
        lc.run_preflight(s, rid, self.secrets, "CHG-UP-1")
        direct.record_phase(s, rid, "IDENTITY_SWITCH", "switch-up")
        w.epoch_issuer = BAOBEI
        w.counts = {"teams": 2, "intake_projects": 5}          # writes after the backup
        direct.apply(self.docker, s, self.secrets, rid, "up", None, "baobei")
        assert direct.pointer(s, "active") == rid

    # -- simulated PostgreSQL side of the disaster recovery ----------------
    def on_write(self, database, sql):
        w = self.world
        if sql.startswith("CREATE DATABASE "):
            w.scratch = {"name": sql.split()[2], "ledger": [], "epoch": None, "privileges": lc.minimal_privileges(),
                         "counts": {}}
        elif sql.startswith("DROP DATABASE IF EXISTS "):
            name = sql.split()[4]
            if name in w.leftover_scratch:
                w.leftover_scratch.remove(name)
            else:
                w.scratch = None
        elif sql.startswith("BEGIN; ALTER DATABASE baobei RENAME TO "):
            view = w.scratch
            w.databases.append(sql.split()[6].rstrip(";"))
            w.ledger, w.privileges, w.counts = view["ledger"], view["privileges"], view["counts"]
            w.epoch_issuer = view["epoch"]
            w.scratch = None
        elif "ALTER TABLE %s OWNER TO baobei_migrator" in sql or sql.startswith("REVOKE ALL ON DATABASE baobei_restore_"):
            pass
        else:
            raise AssertionError(sql)

    def on_restore(self, database):
        meta = json.loads(Path(str(self.backup) + ".json").read_text())
        view = self.world.scratch
        assert view and view["name"] == database
        view["ledger"] = self.restore_ledger if self.restore_ledger is not None else meta["migrations"]
        view["privileges"] = self.restore_privileges
        view["counts"] = self.restore_counts if self.restore_counts is not None else meta["counts"]

    def recover(self, step, backup=None):
        return direct.recover(self.docker, self.state, self.secrets, OLD, step, backup)


class Case(unittest.TestCase):
    def host(self, upgrade=True):
        tmp = TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        h = Host(tmp.name, upgrade)
        self.addCleanup(h.close)
        return h


# ---------------------------------------------------------------- CR-08A
class RealmSwitchWindowTests(Case):
    def test_same_commit_switch_completes_without_fake_phases(self):
        h = self.host()
        w = h.world
        before = (w.migrations, list(w.writes), w.restores, w.pg)
        direct.record_phase(h.state, h.baobei_id, "SMOKE_TEST", "smoke-up")
        direct.record_phase(h.state, h.baobei_id, "RELEASE_COMPLETE", "done-up")
        window = lc.realm_switch(self, h.docker, w, h.state, h.secrets, h.crane_id, "crane")
        self.assertFalse(any(e["phase"] in ("SCHEMA_UPGRADE", "FRESH_BACKUP_VERIFIED", "OLD_API_DRAINED")
                             for e in window["phases"]))
        base = window["phases"][1]
        self.assertEqual(base["kind"], "machine-verified")
        self.assertEqual(base["measured"]["privileges"], "minimal")
        self.assertEqual(base["measured"]["identity"], BAOBEI)       # measured before the switch
        # No implicit migration, database write, restore or PostgreSQL change.
        self.assertEqual((w.migrations, w.writes, w.restores, w.pg), before)
        # The completed window is archived when the next one opens; status is clean.
        lc.realm_switch(self, h.docker, w, h.state, h.secrets, h.baobei_id, "baobei", "CHG-SW2")
        self.assertEqual(len(list((h.state / "maintenance-history").glob("*.json"))), 2)
        report = direct.status_report(h.state)
        self.assertNotIn("attention", report)
        self.assertEqual(report["window"]["status"], "COMPLETE")

    def test_switch_needs_its_own_window_and_measured_baseline(self):
        h = self.host()
        # Without a window, or still inside the unfinished upgrade window.
        with self.assertRaisesRegex(ValueError, "维护窗口"):
            direct.apply(h.docker, h.state, h.secrets, h.crane_id, "up", None, "crane")
        with self.assertRaisesRegex(ValueError, "未完成的维护窗口"):
            direct.record_phase(h.state, h.crane_id, "MAINTENANCE_ENTER", "CHG-X", kind="realm-switch")
        direct.record_phase(h.state, h.baobei_id, "SMOKE_TEST", "smoke-up")
        direct.record_phase(h.state, h.baobei_id, "RELEASE_COMPLETE", "done-up")
        direct.record_phase(h.state, h.crane_id, "MAINTENANCE_ENTER", "CHG-X", kind="realm-switch")
        # The baseline cannot be typed in, and the switch cannot skip it.
        with self.assertRaisesRegex(ValueError, "部署工具"):
            direct.operator_phase_allowed("SWITCH_BASELINE_VERIFIED", "crane", "realm-switch")
        with self.assertRaisesRegex(ValueError, "部署工具"):
            direct.operator_phase_allowed("SCHEMA_UPGRADE", "crane", "realm-switch")
        with self.assertRaisesRegex(ValueError, "阶段顺序"):
            direct.record_phase(h.state, h.crane_id, "IDENTITY_PRECHECK", "pre-1")
        with self.assertRaisesRegex(ValueError, "阶段名|阶段顺序"):
            direct.record_phase(h.state, h.crane_id, "SCHEMA_UPGRADE", "fake-1")
        with self.assertRaisesRegex(ValueError, "阶段顺序"):
            direct.apply(h.docker, h.state, h.secrets, h.crane_id, "up", None, "crane")
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)

    def test_baseline_refuses_wrong_release_ledger_or_privileges(self):
        h = self.host()
        direct.record_phase(h.state, h.baobei_id, "SMOKE_TEST", "smoke-up")
        direct.record_phase(h.state, h.baobei_id, "RELEASE_COMPLETE", "done-up")
        # Active is already the target: not a switch.
        direct.record_phase(h.state, h.baobei_id, "MAINTENANCE_ENTER", "CHG-0", kind="realm-switch")
        with self.assertRaisesRegex(ValueError, "同一提交的另一批准 Realm"):
            direct.realm_switch_baseline(h.docker, h.state, h.secrets, h.baobei_id)
        direct.abort_window(h.state, h.baobei_id, "wrong-target")
        direct.record_phase(h.state, h.crane_id, "MAINTENANCE_ENTER", "CHG-1", kind="realm-switch")
        w = h.world
        good = w.ledger
        w.ledger = good[:-1]                                    # a migration missing in the live DB
        with self.assertRaisesRegex(ValueError, "ledger"):
            direct.realm_switch_baseline(h.docker, h.state, h.secrets, h.crane_id)
        w.ledger = good + [{"name": "99999_extra.sql", "checksum": "0" * 64}]
        with self.assertRaisesRegex(ValueError, "ledger"):
            direct.realm_switch_baseline(h.docker, h.state, h.secrets, h.crane_id)
        w.ledger = good
        for widen in (("roles", "baobei_runtime", "temp", True), ("roles", "baobei_runtime", "create", True),
                      ("roles", "baobei_staff_api", "elevated", True), ("roles", "baobei_runtime", "other_databases", True),
                      ("roles", "baobei_runtime", "schema_create", True), ("public_database", None, None, 1),
                      ("public_relations", None, None, 2), ("foreign_owned", None, None, 1)):
            w.privileges = lc.minimal_privileges()
            if widen[1]:
                w.privileges["roles"][widen[1]][widen[2]] = widen[3]
            else:
                w.privileges[widen[0]] = widen[3]
            with self.subTest(widen=widen), self.assertRaisesRegex(ValueError, "最小集合"):
                direct.realm_switch_baseline(h.docker, h.state, h.secrets, h.crane_id)
        w.privileges = lc.minimal_privileges()
        w.epoch_issuer = CRANE                                   # DB does not trust the active realm
        with self.assertRaisesRegex(ValueError, "身份纪元"):
            direct.realm_switch_baseline(h.docker, h.state, h.secrets, h.crane_id)
        w.epoch_issuer = BAOBEI
        self.assertEqual(direct.next_phase(direct.read_window(h.state)), "SWITCH_BASELINE_VERIFIED")
        direct.realm_switch_baseline(h.docker, h.state, h.secrets, h.crane_id)

    def test_interrupted_start_resumes_and_abort_never_reports_success(self):
        h = self.host()
        direct.record_phase(h.state, h.baobei_id, "SMOKE_TEST", "smoke-up")
        direct.record_phase(h.state, h.baobei_id, "RELEASE_COMPLETE", "done-up")
        rid = h.crane_id
        direct.record_phase(h.state, rid, "MAINTENANCE_ENTER", "CHG-INT", kind="realm-switch")
        direct.realm_switch_baseline(h.docker, h.state, h.secrets, rid)
        direct.record_phase(h.state, rid, "IDENTITY_PRECHECK", "pre-int")
        direct.record_phase(h.state, rid, "IDENTITY_SWITCH", "sw-int")
        h.world.epoch_issuer = CRANE
        real = direct.atomic_json

        def crash_on_active(path, value):
            if path.name == "active.json":
                raise OSError("power loss")
            return real(path, value)
        with patch.object(direct, "atomic_json", crash_on_active), self.assertRaises(OSError):
            direct.apply(h.docker, h.state, h.secrets, rid, "up", None, "crane")
        # Started and recorded, but not active: no success can be recorded.
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)
        self.assertEqual(direct.pointer(h.state, "pending"), rid)
        self.assertIn("attention", direct.status_report(h.state))
        for phase in ("SMOKE_TEST", "RELEASE_COMPLETE"):
            with self.assertRaisesRegex(ValueError, "尚未生效|阶段顺序"):
                direct.record_phase(h.state, rid, phase, "late-" + phase.lower())
        # Retry the same command: resumes, records nothing twice, then completes.
        direct.apply(h.docker, h.state, h.secrets, rid, "up", None, "crane")
        window = direct.read_window(h.state)
        self.assertEqual([e["phase"] for e in window["phases"]].count("NEW_API_START"), 1)
        self.assertEqual(direct.pointer(h.state, "active"), rid)
        self.assertIsNone(direct.pointer(h.state, "pending"))
        direct.record_phase(h.state, rid, "SMOKE_TEST", "smoke-int")
        direct.record_phase(h.state, rid, "RELEASE_COMPLETE", "done-int")
        self.assertEqual(direct.read_window(h.state)["status"], "COMPLETE")

    def test_abort_keeps_pointers_and_frees_the_next_window(self):
        h = self.host()
        rid = h.baobei_id
        active, pending = direct.pointer(h.state, "active"), direct.pointer(h.state, "pending")
        with self.assertRaisesRegex(ValueError, "证据"):
            direct.abort_window(h.state, rid, "")
        with self.assertRaisesRegex(ValueError, "未完成维护窗口"):
            direct.abort_window(h.state, h.crane_id, "abort-1")
        window = direct.abort_window(h.state, rid, "abort-1")
        self.assertEqual((window["status"], window["aborted"]["next_phase"]), ("ABORTED", "SMOKE_TEST"))
        self.assertEqual((direct.pointer(h.state, "active"), direct.pointer(h.state, "pending")), (active, pending))
        with self.assertRaisesRegex(ValueError, "维护窗口"):
            direct.record_phase(h.state, rid, "SMOKE_TEST", "smoke-late")
        direct.record_phase(h.state, h.crane_id, "MAINTENANCE_ENTER", "CHG-NEXT", kind="realm-switch")
        self.assertTrue((h.state / "maintenance-history" / (window["id"] + ".json")).exists())

    def test_legacy_window_files_stay_valid(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            rid = direct.release_id(NEW, "baobei")
            entry = lambda p: {"phase": p, "evidence": "e-" + p.lower(), "at": "2026-11-13T00:00:00+00:00",
                               "kind": "evidence-recorded"}
            # An open window written by the previous tool (no kind, no id): still the upgrade list.
            direct.atomic_json(direct.window_path(state), {"release": rid, "phases": [entry("MAINTENANCE_ENTER")]})
            with self.assertRaisesRegex(ValueError, "未完成"):
                direct.record_phase(state, rid, "MAINTENANCE_ENTER", "CHG-2", kind="realm-switch")
            direct.record_phase(state, rid, "FRESH_BACKUP_VERIFIED", "backup-1")
            self.assertEqual(direct.next_phase(direct.read_window(state)), "OLD_API_DRAINED")
            # A completed old window lets the next one open and is archived unchanged.
            done = {"release": rid, "phases": [entry(p) for p in direct.PHASES]}
            direct.atomic_json(direct.window_path(state), done)
            direct.record_phase(state, NEW, "MAINTENANCE_ENTER", "CHG-3", kind="realm-switch")
            archived = list((state / "maintenance-history").glob("*.json"))
            self.assertEqual(len(archived), 1)
            self.assertEqual(json.loads(archived[0].read_text()), done)


# ---------------------------------------------------------------- CR-08C
class SecretIsolationTests(Case):
    def test_each_realm_renders_its_own_secret_file(self):
        crane, baobei = lc_render("crane"), lc_render("baobei")
        self.assertEqual(crane["secrets"]["oidc_client_secret"]["file"], "/ext/oidc_client_secret")
        self.assertEqual(baobei["secrets"]["oidc_client_secret"]["file"], "/ext/baobei_oidc_client_secret")
        for config, realm in ((crane, "crane"), (baobei, "baobei")):
            env = config["services"]["baobei-api"]["environment"]
            self.assertEqual(env["OIDC_CLIENT_SECRET_FILE"], "/run/secrets/oidc_client_secret")
            direct.check_identity(config, realm)
        bad = lc_render("baobei")
        bad["secrets"]["oidc_client_secret"]["file"] = "/ext/oidc_client_secret"     # shared file under baobei
        with self.assertRaisesRegex(ValueError, "Web 客户端密钥"):
            direct.check_identity(bad, "baobei")
        bad = lc_render("crane")
        bad["services"]["baobei-api"]["environment"]["OIDC_CLIENT_SECRET"] = "inline"
        with self.assertRaisesRegex(ValueError, "Web 客户端密钥"):
            direct.check_identity(bad, "crane")

    def test_dedicated_realm_fails_closed_and_refuses_a_copy(self):
        h = self.host(upgrade=False)
        (h.secrets / "baobei_oidc_client_secret").unlink()
        with self.assertRaisesRegex(ValueError, "baobei_oidc_client_secret"):
            direct.check_mounts(h.secrets, "up", None, "baobei")
        direct.check_mounts(h.secrets, "up", None, "crane")       # the shared realm never needs it
        (h.secrets / "baobei_oidc_client_secret").write_text("synthetic-crane-web\n")   # copy of crane
        with self.assertRaisesRegex(ValueError, "相同"):
            direct.check_mounts(h.secrets, "up", None, "baobei")
        # Environment variables cannot pick the realm or the secret: the tool reads
        # neither, and Docker() drops every BAOBEI_/COMPOSE_ variable before Compose.
        src = Path(direct.__file__).read_text(encoding="utf-8")
        for name in ("OIDC_CLIENT_SECRET", "BAOBEI_IDENTITY_REALM", "BAOBEI_SECRETS_DIR", "BAOBEI_WEB_SECRET"):
            self.assertNotIn('environ.get("' + name, src)
        self.assertIn('"COMPOSE_", "DOCKER_", "BUILDX_", "BAOBEI_"', src)

    def test_precheck_secret_must_be_the_release_secret(self):
        h = self.host(upgrade=False)
        s, rid, w = h.state, h.baobei_id, h.world
        for phase in ("MAINTENANCE_ENTER", "FRESH_BACKUP_VERIFIED", "OLD_API_DRAINED"):
            direct.record_phase(s, rid, phase, "evidence-" + phase.lower())
        w.api_state, w.connections = "exited", 0
        direct.apply(h.docker, s, h.secrets, rid, "migrate", h.secrets / "db_password")
        direct.record_phase(s, rid, "IDENTITY_EPOCH_INIT", "epoch:1:crane")
        # A Web secret file from anywhere else is refused before Keycloak is called.
        other = h.tmp / "elsewhere-secret"
        other.write_text("synthetic-baobei-web")
        with patch.object(direct, "execute_preflight_tool") as run, self.assertRaisesRegex(ValueError, "web-secret-file"):
            direct.preflight(s, rid, lc.ADMIN, "CHG-S", {"web_secret_file": other}, secrets=h.secrets)
        run.assert_not_called()
        with self.assertRaisesRegex(ValueError, "web-secret-file"):
            lc.run_preflight(s, rid, h.secrets, "CHG-S", web_secret=h.secrets / "oidc_client_secret")
        window = lc.run_preflight(s, rid, h.secrets, "CHG-S")
        record = (s / "preflight.json").read_text()
        self.assertNotIn("synthetic-baobei-web", record)            # never the value
        self.assertNotIn("synthetic-baobei-web", json.dumps(window))
        direct.record_phase(s, rid, "IDENTITY_SWITCH", "switch-s")
        w.epoch_issuer = BAOBEI
        # The secret changed after the precheck: the release is refused.
        (h.secrets / "baobei_oidc_client_secret").write_text("synthetic-baobei-web-rotated")
        with self.assertRaisesRegex(ValueError, "身份预检时使用的密钥不一致"):
            direct.apply(h.docker, s, h.secrets, rid, "up", None, "baobei")
        self.assertEqual(direct.pointer(s, "active"), OLD)
        (h.secrets / "baobei_oidc_client_secret").write_text("synthetic-baobei-web")
        direct.apply(h.docker, s, h.secrets, rid, "up", None, "baobei")
        self.assertEqual(w.secret_source, str(h.secrets / "baobei_oidc_client_secret"))

    def test_old_precheck_record_cannot_serve_a_new_window(self):
        h = self.host()
        record = json.loads((h.state / "preflight.json").read_text())
        self.assertIn("window_id", record)
        direct.record_phase(h.state, h.baobei_id, "SMOKE_TEST", "smoke-up")
        direct.record_phase(h.state, h.baobei_id, "RELEASE_COMPLETE", "done-up")
        lc.realm_switch(self, h.docker, h.world, h.state, h.secrets, h.crane_id, "crane", "CHG-BACK")
        # New baobei window, same change number as the original precheck, record still in date.
        rid = h.baobei_id
        direct.record_phase(h.state, rid, "MAINTENANCE_ENTER", "CHG-UP-1", kind="realm-switch")
        direct.realm_switch_baseline(h.docker, h.state, h.secrets, rid)
        direct.atomic_json(h.state / "preflight.json", record)          # the old, sealed record
        with self.assertRaisesRegex(ValueError, "另一个维护窗口"):
            direct.switch_command(h.state, rid, lc.ADMIN, "op-1", "回切", "CHG-UP-1")
        # And wrong realm / admin / change remain refused (existing checks).
        lc.run_preflight(h.state, rid, h.secrets, "CHG-UP-2")
        with self.assertRaisesRegex(ValueError, "变更编号"):
            direct.switch_command(h.state, rid, lc.ADMIN, "op-1", "回切", "CHG-OTHER")
        with self.assertRaisesRegex(ValueError, "预检"):
            direct.switch_command(h.state, rid, "22222222-2222-4222-8222-222222222222", "op-1", "回切", "CHG-UP-2")
        with self.assertRaisesRegex(ValueError, "预检"):
            direct.verify_preflight_record(h.state, h.crane_id, lc.ADMIN)
        direct.switch_command(h.state, rid, lc.ADMIN, "op-1", "回切", "CHG-UP-2")


def lc_render(realm):
    import test_rc0_realm
    return test_rc0_realm.render(realm, "/ext")


# ---------------------------------------------------------------- CR-08B
class DisasterRecoveryTests(Case):
    def enter(self, h):
        direct.abort_window(h.state, h.baobei_id, "abort-for-recovery")
        direct.record_phase(h.state, OLD, "MAINTENANCE_ENTER", "CHG-DR", kind="disaster-recovery")

    def test_full_recovery_moves_pointers_last(self):
        h = self.host()
        w = h.world
        new_ledger = w.ledger
        self.enter(h)
        h.recover("backup", h.backup)
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)
        w.api_state, w.connections = "running", 0
        h.recover("stop")
        self.assertEqual(w.stopped, ["baobei-api"])
        window = h.recover("restore")
        self.assertEqual(w.restores, 1)
        self.assertEqual(w.ledger, w.ledger_of(OLD))
        self.assertTrue(window["phases"][-1]["measured"]["failed_database_kept_as"].startswith("baobei_failed_"))
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)   # not yet
        h.recover("verify")
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)   # not yet
        h.recover("start")
        self.assertEqual(direct.pointer(h.state, "active"), OLD)
        self.assertIsNone(direct.pointer(h.state, "pending"))
        self.assertEqual(w.running["baobei-api"], direct.read_json(h.state / "releases" / OLD / "manifest.json")["images"]["baobei-api"]["id"])
        self.assertEqual(w.secret_source, str(h.secrets / "oidc_client_secret"))   # old secret name
        direct.record_phase(h.state, OLD, "SMOKE_TEST", "smoke-dr")
        window = direct.record_phase(h.state, OLD, "RELEASE_COMPLETE", "done-dr")
        self.assertEqual([e["phase"] for e in window["phases"]], list(direct.RECOVERY_PHASES))
        self.assertNotEqual(w.ledger, new_ledger)
        self.assertEqual(w.migrations, 1)          # no migration was run by the recovery
        # Ordinary rollback rules are unchanged: still refused across contracts.
        self.assertTrue(hasattr(direct, "apply"))

    def test_backup_must_be_the_verified_pre_upgrade_backup(self):
        h = self.host()
        self.enter(h)
        with self.assertRaisesRegex(ValueError, "--backup"):
            h.recover("backup")
        bad = h.make_backup(h.world.ledger, h.world.counts, "post-upgrade.dump")   # new schema
        with self.assertRaisesRegex(ValueError, "升级前备份"):
            h.recover("backup", bad)
        # Same old schema, valid checksum, but weeks older than the upgrade window.
        stale = h.make_backup(h.world.ledger_of(OLD), h.backup_counts, "weeks-old.dump", "2026-10-01T00:00:00+00:00")
        with self.assertRaisesRegex(ValueError, "升级窗口中记录的升级前备份|维护窗口内"):
            h.recover("backup", stale)
        # A fresh one taken now (not the one recorded in FRESH_BACKUP_VERIFIED).
        other = h.make_backup(h.world.ledger_of(OLD), h.backup_counts, "another.dump")
        with self.assertRaisesRegex(ValueError, "升级窗口中记录的升级前备份"):
            h.recover("backup", other)
        tampered = h.make_backup(h.world.ledger_of(OLD), h.backup_counts, "tampered.dump")
        tampered.write_bytes(tampered.read_bytes() + b"x")
        with self.assertRaisesRegex(ValueError, "校验和"):
            h.recover("backup", tampered)
        meta = json.loads(Path(str(h.backup) + ".json").read_text())
        for change in ({"format": "other"}, {"environment": "preview"}, {"database": "crane"}, {"counts": {}}):
            Path(str(h.backup) + ".json").write_text(json.dumps({**meta, **change}))
            with self.subTest(change=change), self.assertRaises(ValueError):
                h.recover("backup", h.backup)
        Path(str(h.backup) + ".json").write_text(json.dumps(meta))
        with self.assertRaisesRegex(ValueError, "绝对路径"):
            h.recover("backup", Path("pre-upgrade.dump"))
        self.assertEqual(len(direct.read_window(h.state)["phases"]), 1)
        h.recover("backup", h.backup)
        # Swapping the archive after it was verified is caught at the restore step.
        h.world.connections = 0
        h.recover("stop")
        h.backup.write_bytes(h.backup.read_bytes() + b"swap")
        with self.assertRaisesRegex(ValueError, "校验和"):
            h.recover("restore")
        self.assertEqual(h.world.restores, 0)

    def test_only_the_rollback_target_of_a_failed_incompatible_release(self):
        h = self.host()
        direct.abort_window(h.state, h.baobei_id, "abort-x")
        direct.record_phase(h.state, h.crane_id, "MAINTENANCE_ENTER", "CHG-DR", kind="disaster-recovery")
        with self.assertRaisesRegex(ValueError, "回滚基线|契约相同|没有需要"):
            direct.recover(h.docker, h.state, h.secrets, h.crane_id, "backup", h.backup)
        # No window of this kind: refused.
        direct.abort_window(h.state, h.crane_id, "abort-y")
        direct.record_phase(h.state, OLD, "MAINTENANCE_ENTER", "CHG-UP", kind="upgrade")
        with self.assertRaisesRegex(ValueError, "disaster-recovery"):
            h.recover("backup", h.backup)

    def test_bad_restore_never_reaches_the_live_database(self):
        for name, setup, message in (
                ("wide privileges", lambda h: h.restore_privileges.update(public_schema=1), "最小集合"),
                ("ledger", lambda h: setattr(h, "restore_ledger", h.world.ledger_of(OLD)[:-1]), "ledger"),
                ("counts", lambda h: setattr(h, "restore_counts", {"teams": 9, "intake_projects": 0}), "计数")):
            with self.subTest(case=name):
                h = self.host()
                live = (list(h.world.ledger), h.world.epoch_issuer)
                self.enter(h)
                h.recover("backup", h.backup)
                h.world.connections = 0
                h.recover("stop")
                setup(h)
                with self.assertRaisesRegex(ValueError, message):
                    h.recover("restore")
                self.assertEqual((h.world.ledger, h.world.epoch_issuer), live)     # untouched
                self.assertIsNone(h.world.scratch)                                 # scratch dropped
                self.assertEqual(direct.next_phase(direct.read_window(h.state)), "DATABASE_RESTORED")
                self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)

    def test_restore_refused_while_the_application_is_connected(self):
        h = self.host()
        self.enter(h)
        h.recover("backup", h.backup)
        h.world.connections = 2
        with self.assertRaisesRegex(ValueError, "业务连接"):
            h.recover("stop")
        self.assertEqual(direct.next_phase(direct.read_window(h.state)), "APP_STOPPED")

    def test_pointers_refused_unless_old_schema_and_old_release_really_run(self):
        h = self.host()
        w = h.world
        self.enter(h)
        h.recover("backup", h.backup)
        w.connections = 0
        h.recover("stop")
        with self.assertRaisesRegex(ValueError, "阶段顺序"):
            h.recover("start")                                   # before restore/verify
        h.recover("restore")
        old_ledger = w.ledger
        w.ledger = w.ledger_of(NEW)                              # someone migrated again
        with self.assertRaisesRegex(ValueError, "ledger"):
            h.recover("verify")
        w.ledger = old_ledger
        w.privileges = lc.minimal_privileges()
        w.privileges["roles"]["baobei_runtime"]["temp"] = True   # widened after restore
        with self.assertRaisesRegex(ValueError, "最小集合"):
            h.recover("verify")
        w.privileges = lc.minimal_privileges()
        h.recover("verify")
        # Unhealthy start: active stays on the failed release, pending says so.
        original = h.docker.compose

        def unhealthy(release, secrets, images, *args, capture=True, realm="crane"):
            if args[0] == "up":
                raise ValueError("docker compose 失败（退出码 1）")
            return original(release, secrets, images, *args, capture=capture, realm=realm)
        with patch.object(h.docker, "compose", unhealthy), self.assertRaisesRegex(ValueError, "退出码"):
            h.recover("start")
        self.assertEqual((direct.pointer(h.state, "active"), direct.pointer(h.state, "pending")),
                         (h.baobei_id, h.baobei_id))
        self.assertIn("attention", direct.status_report(h.state))
        # Running image differs from the old release: refused, pointers unchanged.
        real_start = direct.start

        def wrong_image(docker, release, secrets, manifest, realm):
            real_start(docker, release, secrets, manifest, realm)
            w.running["baobei-api"] = "sha256:" + "9" * 64
        with patch.object(direct, "start", wrong_image), self.assertRaisesRegex(ValueError, "镜像"):
            h.recover("start")
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)
        # Authentication not configured (development login / no OIDC): refused.
        w.auth = {"development": True, "oidc": False}
        with self.assertRaisesRegex(ValueError, "认证"):
            h.recover("start")
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)
        w.auth = {"development": False, "oidc": True}
        # Wrong Web secret source in the running container: refused.
        def shared_file(release, secrets, images, *args, capture=True, realm="crane"):
            result = original(release, secrets, images, *args, capture=capture, realm=realm)
            if args[0] == "up":       # a container started with another secret file
                w.secret_source = str(h.secrets / "baobei_oidc_client_secret")
            return result
        with patch.object(h.docker, "compose", shared_file):
            with self.assertRaisesRegex(ValueError, "Web 密钥来源"):
                h.recover("start")
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)
        h.recover("start")
        self.assertEqual(direct.pointer(h.state, "active"), OLD)
        self.assertIsNone(direct.pointer(h.state, "pending"))

    def test_retry_after_interrupted_swap_does_not_restore_twice(self):
        h = self.host()
        self.enter(h)
        h.recover("backup", h.backup)
        h.world.connections = 0
        h.recover("stop")
        real = direct.record_phase

        def crash(state, rid, phase, *a, **k):
            if phase == "DATABASE_RESTORED":
                raise OSError("operator session lost")
            return real(state, rid, phase, *a, **k)
        with patch.object(direct, "record_phase", crash), self.assertRaises(OSError):
            h.recover("restore")
        self.assertEqual(h.world.restores, 1)
        window = h.recover("restore")
        self.assertEqual(h.world.restores, 1)
        self.assertTrue(window["phases"][-1]["measured"]["already_restored"])

    def test_ordinary_rollback_still_refuses_other_contracts(self):
        h = self.host()
        with self.assertRaisesRegex(ValueError, "数据库契约不同"):
            direct.apply(h.docker, h.state, h.secrets, h.baobei_id, "rollback", None, "crane")
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)


class ReviewFindingTests(Case):
    """Independent review of the first CR-08 draft (P1/P2), each reproduced."""

    def test_same_commit_cross_realm_rollback_cannot_bypass_the_switch_window(self):
        h = self.host()
        direct.record_phase(h.state, h.baobei_id, "SMOKE_TEST", "smoke-up")
        direct.record_phase(h.state, h.baobei_id, "RELEASE_COMPLETE", "done-up")
        lc.realm_switch(self, h.docker, h.world, h.state, h.secrets, h.crane_id, "crane", "CHG-A")
        # NEW+baobei prepared again while NEW (crane) is active: rollback target = NEW (crane).
        import shutil
        shutil.rmtree(h.state / "releases" / h.baobei_id)
        direct.prepare(h.docker, h.state, h.secrets, NEW, "baobei")
        lc.realm_switch(self, h.docker, h.world, h.state, h.secrets, h.baobei_id, "baobei", "CHG-B")
        h.world.epoch_issuer = CRANE
        ups = len(h.world.attempted)
        with self.assertRaisesRegex(ValueError, "realm-switch"):
            direct.apply(h.docker, h.state, h.secrets, h.baobei_id, "rollback", None, "crane")
        self.assertEqual(len(h.world.attempted), ups)
        self.assertEqual(direct.pointer(h.state, "active"), h.baobei_id)

    def test_recovery_crash_after_active_before_pending_cleared_resumes(self):
        h = self.host()
        DisasterRecoveryTests.enter(self, h)
        h.recover("backup", h.backup)
        h.world.connections = 0
        for step in ("stop", "restore", "verify"):
            h.recover(step)
        real = direct.Path.unlink

        def crash(path, *a, **k):
            if path.name == "pending.json":
                raise OSError("power loss")
            return real(path, *a, **k)
        with patch.object(direct.Path, "unlink", crash), self.assertRaises(OSError):
            h.recover("start")
        self.assertEqual((direct.pointer(h.state, "active"), direct.pointer(h.state, "pending")), (OLD, h.baobei_id))
        with self.assertRaisesRegex(ValueError, "尚未生效"):
            direct.record_phase(h.state, OLD, "SMOKE_TEST", "smoke-early")
        h.recover("start")                                   # retry: re-verifies, clears pending
        self.assertEqual((direct.pointer(h.state, "active"), direct.pointer(h.state, "pending")), (OLD, None))
        self.assertEqual([e["phase"] for e in direct.read_window(h.state)["phases"]].count("OLD_API_START"), 1)
        direct.record_phase(h.state, OLD, "SMOKE_TEST", "smoke-dr")
        direct.record_phase(h.state, OLD, "RELEASE_COMPLETE", "done-dr")

    def test_leftover_scratch_from_a_killed_restore_is_cleaned(self):
        h = self.host()
        DisasterRecoveryTests.enter(self, h)
        h.recover("backup", h.backup)
        h.world.connections = 0
        h.recover("stop")
        h.world.leftover_scratch = ["baobei_restore_20261117000000_abcdef"]
        h.recover("restore")
        self.assertEqual(h.world.leftover_scratch, [])
        self.assertIn(("postgres", "DROP DATABASE IF EXISTS baobei_restore_20261117000000_abcdef WITH (FORCE)"),
                      h.world.writes)

    def test_already_restored_needs_the_tools_own_swap(self):
        h = self.host()
        DisasterRecoveryTests.enter(self, h)
        h.recover("backup", h.backup)
        h.world.connections = 0
        h.recover("stop")
        # The live database "looks" restored (old ledger, same counts) but this
        # tool never swapped it: it is restored for real, not shortcut.
        h.world.ledger, h.world.counts = h.world.ledger_of(OLD), dict(h.backup_counts)
        window = h.recover("restore")
        self.assertEqual(h.world.restores, 1)
        self.assertNotIn("already_restored", window["phases"][-1]["measured"])

    def test_precheck_secret_swapped_during_login_is_not_recorded(self):
        h = self.host(upgrade=False)
        s, rid, w = h.state, h.baobei_id, h.world
        direct.record_phase(s, rid, "MAINTENANCE_ENTER", "CHG-1")
        h.backup = h.make_backup(w.ledger, w.counts)
        direct.record_phase(s, rid, "FRESH_BACKUP_VERIFIED", "backup:sha256-" + direct.digest(h.backup.read_bytes())[:16])
        direct.record_phase(s, rid, "OLD_API_DRAINED", "drain-1")
        w.api_state, w.connections = "exited", 0
        direct.apply(h.docker, s, h.secrets, rid, "migrate", h.secrets / "db_password")
        direct.record_phase(s, rid, "IDENTITY_EPOCH_INIT", "epoch:1:crane")

        def login_while_rotating(*a):
            (h.secrets / "baobei_oidc_client_secret").write_text("synthetic-rotated-mid-login")
            return lc.tool_result()
        with patch.object(direct, "execute_preflight_tool", side_effect=login_while_rotating), \
                self.assertRaisesRegex(ValueError, "预检期间 Web 客户端密钥"):
            direct.preflight(s, rid, lc.ADMIN, "CHG-1", {"web_secret_file": h.secrets / "baobei_oidc_client_secret"},
                             secrets=h.secrets)
        self.assertEqual(direct.next_phase(direct.read_window(s)), "IDENTITY_PRECHECK")

    def test_baobei_release_prepared_before_cr08_still_verifies(self):
        h = self.host(upgrade=False)
        rid = h.baobei_id
        release = h.state / "releases" / rid
        manifest = direct.read_json(release / "manifest.json")
        self.assertEqual(manifest["web_secret"], "baobei_oidc_client_secret")
        # Rewrite it as the previous tool prepared it: shared secret file, no web_secret field.
        overlay = release / "compose.realm-baobei.yaml"
        overlay.write_text(overlay.read_text().replace(
            "  oidc_client_secret:\n    file: ${BAOBEI_SECRETS_DIR:?External secret directory}/baobei_oidc_client_secret\n", ""))
        old = {k: v for k, v in manifest.items() if k != "web_secret"}
        old["snapshot_sha256"] = direct.hash_files(release, direct.SNAPSHOT)
        config = json.loads(h.docker.compose(release, h.secrets, old["images"], "config", realm="baobei"))
        old["compose_sha256"] = direct.digest(direct.canonical_json(config))
        direct.atomic_json(release / "manifest.json", old)
        direct.verify_release(h.docker, h.state, h.secrets, rid)
        # …but a new-format manifest claiming the shared file is refused.
        direct.atomic_json(release / "manifest.json", {**old, "web_secret": "baobei_oidc_client_secret"})
        with self.assertRaisesRegex(ValueError, "Web 客户端密钥"):
            direct.verify_release(h.docker, h.state, h.secrets, rid)


class PathAThenPathBTests(Case):
    """R7-D3 counter-proof: the upgrade window belongs to the release id it was
    opened for; path A may switch realms afterwards. Path B from the other realm
    must still find exactly that window and its recorded pre-upgrade backup."""

    def finish_upgrade(self, h, rid):
        direct.record_phase(h.state, rid, "SMOKE_TEST", "smoke-up")
        direct.record_phase(h.state, rid, "RELEASE_COMPLETE", "done-up")

    def recover_all(self, h):
        h.world.connections = 0
        for step in ("stop", "restore", "verify", "start"):
            h.recover(step)
        self.assertEqual((direct.pointer(h.state, "active"), direct.pointer(h.state, "pending")), (OLD, None))
        self.assertEqual(h.world.ledger, h.world.ledger_of(OLD))
        direct.record_phase(h.state, OLD, "SMOKE_TEST", "smoke-dr")
        direct.record_phase(h.state, OLD, "RELEASE_COMPLETE", "done-dr")

    def test_upgrade_on_baobei_path_a_to_crane_then_path_b(self):
        h = self.host()                                   # upgrade window: NEW+baobei
        self.finish_upgrade(h, h.baobei_id)
        lc.realm_switch(self, h.docker, h.world, h.state, h.secrets, h.crane_id, "crane", "CHG-A")
        self.assertEqual(direct.pointer(h.state, "active"), h.crane_id)
        direct.record_phase(h.state, OLD, "MAINTENANCE_ENTER", "CHG-DR", kind="disaster-recovery")
        # Still bound: another backup with the same old schema is refused.
        other = h.make_backup(h.world.ledger_of(OLD), h.backup_counts, "same-schema-other.dump")
        with self.assertRaisesRegex(ValueError, "升级窗口中记录的升级前备份"):
            h.recover("backup", other)
        window = h.recover("backup", h.backup)
        m = window["phases"][-1]["measured"]
        self.assertEqual(m["failed_release"], h.crane_id)
        self.assertEqual(m["upgrade_releases"], sorted([h.crane_id, h.baobei_id]))
        upgrade = next(json.loads(p.read_text()) for p in (h.state / "maintenance-history").glob("*.json")
                       if json.loads(p.read_text())["release"] == h.baobei_id
                       and json.loads(p.read_text()).get("kind") == "upgrade")
        self.assertEqual(m["upgrade_window"], upgrade["id"])
        self.recover_all(h)
        self.assertEqual(h.world.secret_source, str(h.secrets / "oidc_client_secret"))

    def test_reverse_upgrade_on_crane_path_a_to_baobei_then_path_b(self):
        h = Host.__new__(Host)
        tmp = __import__("tempfile").TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        h.__init__(tmp.name, upgrade=False)
        self.addCleanup(h.close)
        s, w, rid = h.state, h.world, h.crane_id
        direct.record_phase(s, rid, "MAINTENANCE_ENTER", "CHG-UP-C")
        h.backup = h.make_backup(w.ledger, w.counts)
        direct.record_phase(s, rid, "FRESH_BACKUP_VERIFIED", "backup:sha256-" + direct.digest(h.backup.read_bytes())[:16])
        direct.record_phase(s, rid, "OLD_API_DRAINED", "drain-1")
        w.api_state, w.connections = "exited", 0
        direct.apply(h.docker, s, h.secrets, rid, "migrate", h.secrets / "db_password")
        w.epoch_issuer = CRANE
        for phase in ("IDENTITY_EPOCH_INIT", "IDENTITY_PRECHECK", "IDENTITY_SWITCH"):
            direct.record_phase(s, rid, phase, "evidence-" + phase.lower())
        w.counts = {"teams": 2, "intake_projects": 5}
        direct.apply(h.docker, s, h.secrets, rid, "up", None)
        self.finish_upgrade(h, rid)
        lc.realm_switch(self, h.docker, w, s, h.secrets, h.baobei_id, "baobei", "CHG-B")
        self.assertEqual(direct.pointer(s, "active"), h.baobei_id)
        direct.record_phase(s, OLD, "MAINTENANCE_ENTER", "CHG-DR", kind="disaster-recovery")
        m = h.recover("backup", h.backup)["phases"][-1]["measured"]
        self.assertEqual((m["failed_release"], m["upgrade_releases"]), (h.baobei_id, sorted([h.crane_id, h.baobei_id])))
        self.recover_all(h)
        # The old release starts with its own (crane) secret, not the baobei one.
        self.assertEqual(h.world.secret_source, str(h.secrets / "oidc_client_secret"))

    def test_sibling_with_another_rollback_target_does_not_lend_its_window(self):
        h = self.host()                                   # upgrade window: NEW+baobei (rollback OLD)
        self.finish_upgrade(h, h.baobei_id)
        lc.realm_switch(self, h.docker, h.world, h.state, h.secrets, h.crane_id, "crane", "CHG-A")
        # Re-prepare NEW+baobei while NEW (crane) is active: its rollback target is now NEW.
        import shutil
        shutil.rmtree(h.state / "releases" / h.baobei_id)
        direct.prepare(h.docker, h.state, h.secrets, NEW, "baobei")
        self.assertEqual(direct.read_json(h.state / "releases" / h.baobei_id / "manifest.json")["rollback_target"], NEW)
        direct.record_phase(h.state, OLD, "MAINTENANCE_ENTER", "CHG-DR", kind="disaster-recovery")
        with self.assertRaisesRegex(ValueError, "找不到失败发布的升级维护窗口"):
            h.recover("backup", h.backup)
        self.assertEqual(len(direct.read_window(h.state)["phases"]), 1)

    def test_another_commits_upgrade_window_is_never_used(self):
        h = self.host()
        self.finish_upgrade(h, h.baobei_id)
        lc.realm_switch(self, h.docker, h.world, h.state, h.secrets, h.crane_id, "crane", "CHG-A")
        # Rewrite the archived upgrade window as if it belonged to another commit.
        for p in (h.state / "maintenance-history").glob("*.json"):
            window = json.loads(p.read_text())
            if window.get("kind") == "upgrade":
                window["release"] = direct.release_id("d" * 40, "baobei")
                p.write_text(json.dumps(window))
        direct.record_phase(h.state, OLD, "MAINTENANCE_ENTER", "CHG-DR", kind="disaster-recovery")
        with self.assertRaisesRegex(ValueError, "找不到失败发布的升级维护窗口"):
            h.recover("backup", h.backup)


class PrivilegeSqlTests(unittest.TestCase):
    def test_privilege_query_covers_the_minimal_set(self):
        sql = direct.privileges_sql("baobei")
        for needle in ("rolsuper", "pg_auth_members", "'CONNECT'", "'CREATE'", "'TEMP'", "has_schema_privilege",
                       "grantee = 0", "baobei_migrator"):
            self.assertIn(needle, sql)
        self.assertEqual(set(direct.APP_ROLES), {"baobei_migrator", "baobei_runtime", "baobei_maintenance",
                                                 "baobei_staff_api"})
        self.assertTrue(direct.check_privileges(lc.minimal_privileges()))


if __name__ == "__main__":
    unittest.main()
