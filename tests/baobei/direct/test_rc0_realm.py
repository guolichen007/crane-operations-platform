"""G5-RC0 release-chain checks for direct-deploy.py (no Docker, no production).

P0-DEPLOY-01: a standard direct deployment must be able to select the approved
dedicated realm explicitly; the realm overlay and the account-service secret must
be part of the verified release snapshot, the manifest and the config hash.
"""
import importlib.util
import json
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
ENTRY = ROOT / "deploy/baobei/direct-deploy.py"
SPEC = importlib.util.spec_from_file_location("direct_deploy_rc0", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)
CURRENT = "a" * 40


class RealmSelectionTests(unittest.TestCase):
    def test_realm_overlay_is_in_the_release_snapshot(self):
        self.assertIn("compose.realm-baobei.yaml", direct.SNAPSHOT,
                      "the dedicated-realm overlay is not part of the verified release snapshot")

    def test_compose_command_loads_overlay_only_for_baobei(self):
        calls = []

        class Rec(direct.Docker):
            def __init__(self):
                self.env = {}

            def call(self, *args, capture=True, env=None, cwd=None):
                calls.append(args)
                return b"{}"
        images = {s: {"ref": s, "id": "sha256:" + "a" * 64} for s in direct.SERVICES}
        d = Rec()
        d.compose(Path("/r"), Path("/s"), images, "config", realm="crane")
        d.compose(Path("/r"), Path("/s"), images, "config", realm="baobei")
        crane, baobei = calls
        self.assertNotIn("compose.realm-baobei.yaml", crane)
        self.assertIn("compose.realm-baobei.yaml", baobei)

    def test_baobei_requires_account_service_secret(self):
        self.assertIn("account_client_secret", direct.realm_secrets("baobei"))
        self.assertNotIn("account_client_secret", direct.realm_secrets("crane"))

    def test_manifest_binds_realm(self):
        source = ENTRY.read_text(encoding="utf-8")
        self.assertIn('"identity_realm"', source, "the manifest does not record the identity realm")


def render(realm, secrets="/ext/secrets"):
    """Approximate `docker compose config` for the snapshot files: YAML merge of
    production + direct (+ realm overlay), with ${BAOBEI_SECRETS_DIR} resolved.
    The real Compose rendering is asserted in run.py (CI direct-check, Docker)."""
    import yaml

    def merge(a, b):
        if isinstance(a, dict) and isinstance(b, dict):
            out = dict(a)
            for k, v in b.items():
                out[k] = merge(a[k], v) if k in a and k != "secrets" else v
            return out
        return b
    files = ["compose.production.yaml", "compose.direct.yaml"]
    overlay = direct.realm_profile(realm)["overlay"]
    if overlay:
        files.append(overlay)
    config = {}
    for name in files:
        text = (ROOT / "deploy/baobei" / name).read_text(encoding="utf-8")
        text = text.replace("${BAOBEI_SECRETS_DIR:?External secret directory}", secrets)
        config = merge(config, yaml.safe_load(text))
    return config


class IdentityConfigTests(unittest.TestCase):
    def test_default_release_stays_crane(self):
        config = render("crane")
        direct.check_identity(config, "crane")
        env = config["services"]["baobei-api"]["environment"]
        self.assertEqual(env["BAOBEI_IDENTITY_REALM"], "crane")
        self.assertNotIn("BAOBEI_ACCOUNT_CLIENT_ID", env)
        self.assertNotIn("account_client_secret", config["services"]["baobei-api"]["secrets"])

    def test_baobei_release_wires_issuer_client_and_secret_mount(self):
        config = render("baobei")
        direct.check_identity(config, "baobei")
        api = config["services"]["baobei-api"]
        self.assertEqual(api["environment"]["OIDC_ISSUER"], "https://crane.youdaozd.com/realms/baobei")
        self.assertEqual(api["environment"]["BAOBEI_ACCOUNT_CLIENT_ID"], "baobei-account-service")
        self.assertEqual(api["environment"]["BAOBEI_ACCOUNT_CLIENT_SECRET_FILE"], "/run/secrets/account_client_secret")
        self.assertIn("account_client_secret", api["secrets"])
        self.assertEqual(config["secrets"]["account_client_secret"]["file"], "/ext/secrets/account_client_secret")

    def test_mismatches_fail_closed(self):
        with self.assertRaises(ValueError):          # crane config claimed as baobei
            direct.check_identity(render("crane"), "baobei")
        with self.assertRaises(ValueError):          # baobei config claimed as crane
            direct.check_identity(render("baobei"), "crane")
        for change in ({"OIDC_ISSUER": "https://crane.youdaozd.com/realms/crane"},
                       {"BAOBEI_ACCOUNT_CLIENT_ID": "admin-cli"},
                       {"BAOBEI_IDENTITY_REALM": "crane"},
                       {"OIDC_CLIENT_ID": "other"}):
            config = render("baobei")
            config["services"]["baobei-api"]["environment"].update(change)
            with self.subTest(change=change), self.assertRaises(ValueError):
                direct.check_identity(config, "baobei")
        config = render("baobei")
        config["services"]["baobei-api"]["secrets"].remove("account_client_secret")
        with self.assertRaises(ValueError):
            direct.check_identity(config, "baobei")
        config = render("baobei")
        config["secrets"]["account_client_secret"]["file"] = "/tmp/elsewhere"
        with self.assertRaises(ValueError):
            direct.check_identity(config, "baobei")
        with self.assertRaises(ValueError):
            direct.realm_profile("baobei-preview")
        with self.assertRaises(ValueError):
            direct.release_id(CURRENT, "master")

    def test_missing_account_secret_is_refused_before_docker(self):
        with TemporaryDirectory() as tmp:
            secrets = Path(tmp).resolve()
            for name in direct.SECRET_NAMES:
                (secrets / name).touch()
            with patch.object(direct, "WEBROOT", secrets):
                direct.check_mounts(secrets, "up", None, "crane")
                with self.assertRaisesRegex(ValueError, "account_client_secret"):
                    direct.check_mounts(secrets, "up", None, "baobei")
                (secrets / "account_client_secret").touch()
                direct.check_mounts(secrets, "up", None, "baobei")

    def test_environment_cannot_select_the_realm(self):
        # No BAOBEI_* variable reaches Compose, and the realm only comes from the
        # verified manifest/release id: there is no env override in the code.
        source = ENTRY.read_text(encoding="utf-8")
        self.assertNotIn('environ.get("BAOBEI_IDENTITY_REALM"', source)
        self.assertNotIn("environ['BAOBEI_IDENTITY_REALM'", source)


class ManifestTests(unittest.TestCase):
    """verify_release against a synthetic release directory (Docker faked)."""

    def make(self, state, realm):
        rid = direct.release_id(CURRENT, realm)
        release = state / "releases" / rid
        release.mkdir(parents=True)
        for name in direct.SNAPSHOT:
            (release / name).parent.mkdir(parents=True, exist_ok=True)
            (release / name).write_bytes((ROOT / "deploy/baobei" / name).read_bytes())
        images = {s: {"ref": direct.local_ref(s, CURRENT), "id": "sha256:" + "c" * 64, "created_at": "x"} for s in direct.SERVICES}
        manifest = {"mode": "direct-local", "source_sha": CURRENT, "source": direct.SOURCE, "platform": "linux/amd64",
                    "images": images, "identity_realm": realm, "identity_issuer": direct.REALMS[realm]["issuer"],
                    "compose_sha256": "h-" + realm, "snapshot_sha256": direct.hash_files(release, direct.SNAPSHOT),
                    "database_contract_sha256": "db", "rollback_target": None}
        direct.atomic_json(release / "manifest.json", manifest)
        return rid, release, manifest

    def docker(self):
        class D:
            platform = "linux/amd64"

            def inspect(self, ref, source):
                return {"ref": ref, "id": "sha256:" + "c" * 64, "created_at": "x"}
        return D()

    def test_tampering_is_refused(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            rid, release, manifest = self.make(state, "baobei")
            with patch.object(direct, "config_hash", side_effect=lambda d, r, s, i, realm: "h-" + realm):
                direct.verify_release(self.docker(), state, state, rid)
                # 1. overlay edited after prepare → snapshot hash mismatch
                original = (release / "compose.realm-baobei.yaml").read_text()
                (release / "compose.realm-baobei.yaml").write_text(original.replace("baobei-account-service", "admin-cli"))
                with self.assertRaises(ValueError):
                    direct.verify_release(self.docker(), state, state, rid)
                (release / "compose.realm-baobei.yaml").write_text(original)
                # 2. manifest realm or issuer edited → refused
                for change in ({"identity_realm": "crane"}, {"identity_issuer": "https://crane.youdaozd.com/realms/crane"}):
                    direct.atomic_json(release / "manifest.json", {**manifest, **change})
                    with self.subTest(change=change), self.assertRaises(ValueError):
                        direct.verify_release(self.docker(), state, state, rid)
                direct.atomic_json(release / "manifest.json", manifest)
                # 3. the baobei release cannot be used under the crane release id
                (state / "releases" / CURRENT).symlink_to(release)
                with self.assertRaises(ValueError):
                    direct.verify_release(self.docker(), state, state, CURRENT)
            # 4. config hash bound to the realm
            with patch.object(direct, "config_hash", return_value="h-crane"):
                with self.assertRaisesRegex(ValueError, "Compose 配置哈希"):
                    direct.verify_release(self.docker(), state, state, rid)

    def test_pre_rc0_release_still_verifies_as_crane(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            rid, release, manifest = self.make(state, "crane")
            legacy = {k: v for k, v in manifest.items() if k not in ("identity_realm", "identity_issuer")}
            (release / "compose.realm-baobei.yaml").unlink()
            legacy["snapshot_sha256"] = direct.hash_files(release, direct.snapshot_names(legacy))
            direct.atomic_json(release / "manifest.json", legacy)
            with patch.object(direct, "config_hash", side_effect=lambda d, r, s, i, realm: "h-" + realm):
                direct.verify_release(self.docker(), state, state, rid)


class OrderGateTests(unittest.TestCase):
    def test_phases_in_order_with_evidence(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            rid = direct.release_id(CURRENT, "baobei")
            with self.assertRaises(ValueError):
                direct.record_phase(state, rid, "FRESH_BACKUP_VERIFIED", "backup-1")
            direct.record_phase(state, rid, "MAINTENANCE_ENTER", "CHG-1")
            with self.assertRaises(ValueError):          # skipping the backup
                direct.record_phase(state, rid, "OLD_API_DRAINED", "drain-1")
            with self.assertRaises(ValueError):          # evidence required
                direct.record_phase(state, rid, "FRESH_BACKUP_VERIFIED", "")
            with self.assertRaises(ValueError):          # other release
                direct.record_phase(state, CURRENT, "FRESH_BACKUP_VERIFIED", "backup-1")
            direct.record_phase(state, rid, "FRESH_BACKUP_VERIFIED", "backup-20261113:sha256-abcd")
            with self.assertRaises(ValueError):
                direct.require_phase(state, rid, "SCHEMA_UPGRADE")   # drain missing
            direct.record_phase(state, rid, "OLD_API_DRAINED", "drain:0-sessions")
            direct.require_phase(state, rid, "SCHEMA_UPGRADE")
            with self.assertRaises(ValueError):
                direct.record_phase(state, rid, "MAINTENANCE_ENTER", "CHG-2")  # window still open

    def apply_case(self, active_rid, rid, command, confirm=None, contracts=("db", "db"), phases=()):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            direct.atomic_json(state / "active.json", {"source_sha": active_rid})
            for phase in phases:
                direct.record_phase(state, rid, phase, "evidence-" + phase.lower())
            releases = {
                active_rid: (state / active_rid, {"rollback_target": None, "database_contract_sha256": contracts[0], "images": {}}),
                rid: (state / rid, {"rollback_target": active_rid, "database_contract_sha256": contracts[1],
                                    "images": {s: {"ref": s, "id": "sha256:" + "e" * 64} for s in direct.SERVICES}}),
            }
            calls = []
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "verify_release", side_effect=lambda d, s, k, r: releases[r]), \
                 patch.object(direct, "start", side_effect=lambda *a: calls.append(a)):
                direct.apply(object(), state, state, rid, command, None, confirm)
            return calls, direct.pointer(state, "active")

    def test_realm_change_needs_explicit_confirmation(self):
        new = direct.release_id(CURRENT, "baobei")
        old = "b" * 40
        with self.assertRaisesRegex(ValueError, "--confirm-realm"):
            self.apply_case(old, new, "up")
        with self.assertRaisesRegex(ValueError, "--confirm-realm"):
            self.apply_case(old, new, "up", confirm="crane")
        calls, active = self.apply_case(old, new, "up", confirm="baobei")
        self.assertEqual(active, new)
        self.assertEqual(calls[0][-1], "baobei")   # started with the baobei overlay

    def test_incompatible_upgrade_needs_the_window(self):
        old, new = "b" * 40, CURRENT
        with self.assertRaisesRegex(ValueError, "维护窗口"):
            self.apply_case(old, new, "up", contracts=("old", "new"))
        before = direct.PHASES[:direct.PHASES.index("NEW_API_START")]
        calls, active = self.apply_case(old, new, "up", contracts=("old", "new"), phases=before)
        self.assertEqual(active, new)
        # compatible upgrades keep the existing one-step path
        calls, active = self.apply_case(old, new, "up")
        self.assertEqual(active, new)

    def test_path_a_same_sha_back_to_crane(self):
        # Keep the new schema and image, only switch the realm back: same commit,
        # crane release, same database contract → allowed with confirmation.
        new = direct.release_id(CURRENT, "baobei")
        back = direct.release_id(CURRENT, "crane")
        with TemporaryDirectory() as tmp:
            pass
        with self.assertRaisesRegex(ValueError, "--confirm-realm"):
            self.apply_case(new, back, "up")
        calls, active = self.apply_case(new, back, "up", confirm="crane")
        self.assertEqual((active, calls[0][-1]), (back, "crane"))

    def test_rollback_across_contracts_still_refused(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            new, old = direct.release_id(CURRENT, "baobei"), "b" * 40
            direct.atomic_json(state / "active.json", {"source_sha": new})
            releases = {new: (state / new, {"rollback_target": old, "database_contract_sha256": "new", "images": {}}),
                        old: (state / old, {"rollback_target": None, "database_contract_sha256": "old", "images": {}})}
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "verify_release", side_effect=lambda d, s, k, r: releases[r]), \
                 patch.object(direct, "start") as start:
                with self.assertRaisesRegex(ValueError, "数据库契约不同"):
                    direct.apply(object(), state, state, new, "rollback", None, "crane")
                start.assert_not_called()
            self.assertEqual(direct.pointer(state, "active"), new)


if __name__ == "__main__":
    unittest.main()
