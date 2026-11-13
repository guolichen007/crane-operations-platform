"""G5-RC0-R1 release lifecycle checks (no Docker, no production).

The deployment tool runs for real against a temporary state directory: real
prepare (snapshot, manifests, hashes), real verify_release and apply. Only the
outside world is simulated: git (export_source / verify_source), docker build /
image inspect, and `docker compose` (config is rendered by merging the snapshot
YAML files; up/ps/inspect model one PostgreSQL container that must never be
recreated). The real Compose rendering is asserted by run.py in CI.
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
SPEC = importlib.util.spec_from_file_location("direct_deploy_lifecycle", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)

OLD, NEW = "253b4a8" + "0" * 33, "c" * 40
MIGRATIONS = ROOT / "services/project-intake-api/migrations"


def merge(a, b):
    if isinstance(a, dict) and isinstance(b, dict):
        out = dict(a)
        for k, v in b.items():
            out[k] = merge(a[k], v) if k in a and k != "secrets" else v
        return out
    return b


class World:
    """Simulated host: git trees per commit, local images, one PostgreSQL."""

    def __init__(self, tmp):
        self.tmp = tmp
        self.images = {}          # ref -> image id
        self.running = {}         # service -> image id
        self.pg = ("pg-1", "2026-11-13T00:00:00Z")
        self.ups = []
        self.epoch_issuer = None  # what the database trusts (identity_epoch)
        self.api_state = "running"  # old API container
        self.connections = 3        # baobei_runtime / baobei_staff_api sessions
        self.migrations = 0
        self.attempted = []         # every compose up, also those that fail
        self.trees = {OLD: self.tree(OLD, upto="00005"), NEW: self.tree(NEW, upto=None)}

    def tree(self, sha, upto):
        root = self.tmp / ("src-" + sha[:7])
        shutil.copytree(ROOT / "deploy/baobei", root / "deploy/baobei",
                        ignore=shutil.ignore_patterns("__pycache__"))
        mig = root / "services/project-intake-api/migrations"
        mig.mkdir(parents=True)
        for f in sorted(MIGRATIONS.glob("*.sql")):
            if upto is None or f.name < upto + "z":
                shutil.copyfile(f, mig / f.name)
        shutil.copyfile(ROOT / "services/project-intake-api/database-grants.sql",
                        root / "services/project-intake-api/database-grants.sql")
        if sha == OLD:   # the old release had no realm overlay
            (root / "deploy/baobei/compose.realm-baobei.yaml").unlink()
        return root

    # -- git / build --------------------------------------------------------
    def export_source(self, source, dest):
        shutil.copytree(self.trees[source], dest, dirs_exist_ok=True)

    def build_images(self, docker, source, staged, go_proxy):
        out = {}
        for service in direct.SERVICES:
            ref = direct.local_ref(service, source)
            iid = "sha256:" + (source[:1] + service[-1]) * 32
            self.images[ref] = iid
            out[service] = {"ref": ref, "id": iid, "created_at": "synthetic"}
        return out


class FakeDocker:
    platform = "linux/amd64"

    def __init__(self, world):
        self.w = world

    def inspect(self, ref, source):
        if ref not in self.w.images:
            raise ValueError("本地镜像不存在")
        return {"ref": ref, "id": self.w.images[ref], "created_at": "synthetic"}

    def compose(self, release, secrets, images, *args, capture=True, realm="crane"):
        if args[0] == "config":
            files = ["compose.production.yaml", "compose.direct.yaml"]
            overlay = direct.realm_profile(realm)["overlay"]
            if overlay:
                files.append(overlay)
            import yaml
            config = {}
            for name in files:
                text = (release / name).read_text(encoding="utf-8")
                text = text.replace("${BAOBEI_SECRETS_DIR:?External secret directory}", str(secrets))
                text = text.replace("${BAOBEI_WEB_IMAGE:?Pin the approved web image digest}", images["baobei-web"]["ref"])
                text = text.replace("${BAOBEI_API_IMAGE:?Pin the approved api image digest}", images["baobei-api"]["ref"])
                config = merge(config, yaml.safe_load(text))
            return json.dumps(config).encode()
        if args[0] == "up":
            assert "--no-deps" in args and "baobei-postgres" not in args, args
            self.w.attempted.append(realm)
            # The API refuses to start unless the database trusts its issuer.
            if self.w.epoch_issuer != direct.REALMS[realm]["issuer"]:
                raise ValueError("docker compose 失败（API 身份纪元不匹配，退出码 1）")
            for service in direct.SERVICES:
                self.w.running[service] = images[service]["id"]
            self.w.ups.append((realm, images["baobei-api"]["id"]))
            return None
        if args[:2] == ("ps", "-q"):
            return (args[2] + "-container\n").encode()
        if args[:3] == ("ps", "-a", "-q"):
            return (args[3] + "-container\n").encode()
        raise AssertionError(args)

    def call(self, *args, capture=True, env=None, cwd=None):
        if args[0] == "run":                     # the migrator
            self.w.migrations += 1
            return None
        if args[0] == "exec":                    # read-only checks
            sql = args[-1]
            assert sql.startswith("BEGIN READ ONLY;"), sql
            if "identity_epoch" in sql:
                return (self.w.epoch_issuer or "").encode()
            if "pg_stat_activity" in sql:
                return str(self.w.connections).encode()
            raise AssertionError(sql)
        assert args[0] == "inspect", args
        container = args[-1]
        if args[2] == "{{.State.Status}}":
            return self.w.api_state.encode()
        if container == "baobei-postgres-container":
            return f"{self.w.pg[0]} {self.w.pg[1]} healthy".encode()
        return (self.w.running[container.removesuffix("-container")] + "\n").encode()


def prepare_old(docker, state, secrets):
    """The currently deployed pre-RC0 release: manifest without identity fields."""
    with patch.object(direct, "SNAPSHOT", tuple(n for n in direct.SNAPSHOT if n != "compose.realm-baobei.yaml")):
        direct.prepare(docker, state, secrets, OLD, "crane")
    path = state / "releases" / OLD / "manifest.json"
    manifest = json.loads(path.read_text())
    manifest.pop("identity_realm")
    manifest.pop("identity_issuer")
    manifest["snapshot_sha256"] = direct.hash_files(state / "releases" / OLD, direct.snapshot_names(manifest))
    direct.atomic_json(path, manifest)
    direct.atomic_json(state / "active.json", {"source_sha": OLD})


class PathATests(unittest.TestCase):
    def test_prepare_both_realms_then_switch_back_to_crane(self):
        with TemporaryDirectory() as tmp:
            tmp = Path(tmp).resolve()
            world = World(tmp)
            state, secrets = tmp / "state", tmp / "secrets"
            state.mkdir(mode=0o700)
            secrets.mkdir()
            for name in direct.SECRET_NAMES + ("account_client_secret",):
                (secrets / name).write_text("synthetic")
            docker = FakeDocker(world)
            with patch.object(direct, "verify_source"), patch.object(direct, "export_source", world.export_source), \
                 patch.object(direct, "build_images", world.build_images), patch.object(direct, "WEBROOT", tmp), \
                 patch.object(direct.sys, "platform", "linux"):
                # 1. Production today: the old release (crane) is active.
                world.epoch_issuer = direct.REALMS["crane"]["issuer"]   # (pre-00006 the old API has no epoch; modelled as trusted)
                with patch.object(direct, "SNAPSHOT", tuple(n for n in direct.SNAPSHOT if n != "compose.realm-baobei.yaml")):
                    direct.prepare(docker, state, secrets, OLD, "crane")
                old_manifest = json.loads((state / "releases" / OLD / "manifest.json").read_text())
                old_manifest.pop("identity_realm")
                old_manifest.pop("identity_issuer")
                old_manifest["snapshot_sha256"] = direct.hash_files(state / "releases" / OLD, direct.snapshot_names(old_manifest))
                direct.atomic_json(state / "releases" / OLD / "manifest.json", old_manifest)
                direct.atomic_json(state / "active.json", {"source_sha": OLD})
                # 2. Prepare the new commit for both realms while OLD is active.
                direct.prepare(docker, state, secrets, NEW, "crane")
                direct.prepare(docker, state, secrets, NEW, "baobei")
                crane_id, baobei_id = NEW, direct.release_id(NEW, "baobei")
                for rid in (crane_id, baobei_id):
                    _, m = direct.verify_release(docker, state, secrets, rid)
                    # 3. Both manifests keep the real original rollback target.
                    self.assertEqual(m["rollback_target"], OLD)
                # 4–5. Maintenance window, schema upgrade, switch to baobei, start.
                for phase in direct.PHASES[:direct.PHASES.index("SCHEMA_UPGRADE")]:
                    direct.record_phase(state, baobei_id, phase, "evidence-" + phase.lower())
                # Evidence says "drained", but the machine check still sees the old API.
                with self.assertRaisesRegex(ValueError, "旧 baobei-api 仍处于 running"):
                    direct.apply(docker, state, secrets, baobei_id, "migrate", secrets / "db_password")
                world.api_state = "exited"
                with self.assertRaisesRegex(ValueError, "仍有 3 个业务连接"):
                    direct.apply(docker, state, secrets, baobei_id, "migrate", secrets / "db_password")
                self.assertEqual(world.migrations, 0)
                world.connections = 0
                direct.apply(docker, state, secrets, baobei_id, "migrate", secrets / "db_password")
                self.assertEqual(world.migrations, 1)
                window = direct.read_window(state)
                kinds = {e["phase"]: e["kind"] for e in window["phases"]}
                self.assertEqual(kinds["OLD_API_DRAINED"], "evidence-recorded")
                self.assertEqual(kinds["SCHEMA_UPGRADE"], "machine-verified")
                self.assertEqual(window["phases"][-1]["measured"]["runtime_drained"]["business_connections"], 0)
                world.epoch_issuer = None   # 00006 created identity_epoch, still empty
                for phase in ("IDENTITY_EPOCH_INIT", "IDENTITY_PRECHECK", "IDENTITY_SWITCH"):
                    direct.record_phase(state, baobei_id, phase, "evidence-" + phase.lower())
                # Starting baobei before the epoch trusts baobei is refused.
                with self.assertRaisesRegex(ValueError, "身份纪元为空或未指向目标 Realm"):
                    direct.apply(docker, state, secrets, baobei_id, "up", None, "baobei")
                world.epoch_issuer = direct.REALMS["baobei"]["issuer"]
                direct.apply(docker, state, secrets, baobei_id, "up", None, "baobei")
                self.assertEqual(direct.pointer(state, "active"), baobei_id)
                pg_before, api_new = world.pg, world.running["baobei-api"]
                # 6. Identity epoch switched back to crane; same-SHA crane release.
                with self.assertRaisesRegex(ValueError, "身份纪元为空或未指向目标 Realm"):
                    direct.apply(docker, state, secrets, crane_id, "up", None, "crane")   # epoch still baobei
                world.epoch_issuer = direct.REALMS["crane"]["issuer"]
                with self.assertRaisesRegex(ValueError, "--confirm-realm"):
                    direct.apply(docker, state, secrets, crane_id, "up", None)
                direct.apply(docker, state, secrets, crane_id, "up", None, "crane")
                # 7. Back on crane, same new images and schema, PostgreSQL untouched.
                self.assertEqual(direct.pointer(state, "active"), crane_id)
                self.assertEqual(world.running["baobei-api"], api_new)
                self.assertEqual(world.pg, pg_before)
                self.assertEqual(world.ups[-1][0], "crane")
                # Switching to baobei again (same commit) is the same controlled path.
                world.epoch_issuer = direct.REALMS["baobei"]["issuer"]
                direct.apply(docker, state, secrets, baobei_id, "up", None, "baobei")
                self.assertEqual(direct.pointer(state, "active"), baobei_id)
                # The old image on the old schema contract stays refused (path B only).
                with self.assertRaisesRegex(ValueError, "数据库契约不同"):
                    direct.apply(docker, state, secrets, baobei_id, "rollback", None, "crane")
                self.assertEqual(direct.pointer(state, "active"), baobei_id)
                self.assertEqual(world.pg, pg_before)

    def test_same_commit_switch_requires_identical_images_and_contract(self):
        with TemporaryDirectory() as tmp:
            tmp = Path(tmp).resolve()
            state = tmp / "state"
            state.mkdir(mode=0o700)
            a, b = direct.release_id(NEW, "baobei"), NEW
            direct.atomic_json(state / "active.json", {"source_sha": a})
            base = {"rollback_target": OLD, "database_contract_sha256": "db",
                    "images": {s: {"ref": s, "id": "sha256:" + "e" * 64} for s in direct.SERVICES}}
            for change in ({"database_contract_sha256": "other"},
                           {"images": {s: {"ref": s, "id": "sha256:" + "f" * 64} for s in direct.SERVICES}}):
                releases = {a: (state / a, base), b: (state / b, {**base, **change})}
                with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                     patch.object(direct, "verify_release", side_effect=lambda d, s, k, r: releases[r]), \
                     patch.object(direct, "start") as start, self.subTest(change=list(change)):
                    with self.assertRaisesRegex(ValueError, "同提交 Realm 切换要求相同镜像与数据库契约"):
                        direct.apply(object(), state, state, b, "up", None, "crane")
                    start.assert_not_called()
            # A different commit on another realm still needs the rollback baseline.
            other = "d" * 40
            releases = {a: (state / a, base), other: (state / other, {**base, "rollback_target": "e" * 40})}
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "verify_release", side_effect=lambda d, s, k, r: releases[r]), \
                 patch.object(direct, "start") as start:
                with self.assertRaisesRegex(ValueError, "回滚基线已过期"):
                    direct.apply(object(), state, state, other, "up", None, "crane")


if __name__ == "__main__":
    unittest.main()


class FirstEpochTests(unittest.TestCase):
    """Production 00005 → 00008: identity_epoch exists but is empty after the
    migration. The tool must not start any API on an empty epoch, and the
    window must contain an explicit IDENTITY_EPOCH_INIT before the precheck."""

    def test_window_has_epoch_init_before_precheck(self):
        self.assertIn("IDENTITY_EPOCH_INIT", direct.PHASES)
        self.assertLess(direct.PHASES.index("SCHEMA_UPGRADE"), direct.PHASES.index("IDENTITY_EPOCH_INIT"))
        self.assertLess(direct.PHASES.index("IDENTITY_EPOCH_INIT"), direct.PHASES.index("IDENTITY_PRECHECK"))

    def test_up_on_empty_epoch_is_refused_before_compose(self):
        with TemporaryDirectory() as tmp:
            tmp = Path(tmp).resolve()
            world = World(tmp)
            world.api_state, world.connections = "exited", 0
            state, secrets = tmp / "state", tmp / "secrets"
            state.mkdir(mode=0o700)
            secrets.mkdir()
            for name in direct.SECRET_NAMES:
                (secrets / name).write_text("synthetic")
            docker = FakeDocker(world)
            with patch.object(direct, "verify_source"), patch.object(direct, "export_source", world.export_source), \
                 patch.object(direct, "build_images", world.build_images), patch.object(direct, "WEBROOT", tmp), \
                 patch.object(direct.sys, "platform", "linux"):
                world.epoch_issuer = direct.REALMS["crane"]["issuer"]
                prepare_old(docker, state, secrets)
                direct.prepare(docker, state, secrets, NEW, "crane")
                world.epoch_issuer = None
                for phase in direct.PHASES[:direct.PHASES.index("NEW_API_START")]:
                    if phase != "SCHEMA_UPGRADE":
                        direct.record_phase(state, NEW, phase, "evidence-" + phase.lower())
                    else:
                        direct.apply(docker, state, secrets, NEW, "migrate", secrets / "db_password")
                world.epoch_issuer = None            # migrated, epoch never initialized
                with self.assertRaisesRegex(ValueError, "身份纪元"):
                    direct.apply(docker, state, secrets, NEW, "up", None)
                self.assertEqual(world.attempted, [])  # refused before Compose touched any container
                self.assertEqual(direct.pointer(state, "active"), OLD)
                world.epoch_issuer = direct.REALMS["crane"]["issuer"]
                direct.apply(docker, state, secrets, NEW, "up", None)
                self.assertEqual(direct.pointer(state, "active"), NEW)


# G5-RC1: the former PreflightReportTests accepted a hand-written PASS JSON
# (staffId "s"); that path is removed. Provenance is tested in test_rc1_preflight.py.
