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

OLD, NEW = "7b9558084d73f5210cdd08e8240883ecc1659de8", "c" * 40   # OLD = deployed production commit
MIGRATIONS = ROOT / "services/project-intake-api/migrations"


def merge(a, b):
    if isinstance(a, dict) and isinstance(b, dict):
        out = dict(a)
        for k, v in b.items():
            out[k] = merge(a[k], v) if k in a and isinstance(v, dict) else v
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
        # CR-08: what the tool reads from the database and the containers.
        self.ledger = self.ledger_of(OLD)
        self.privileges = minimal_privileges()
        self.counts = {"teams": 1, "intake_projects": 0}
        self.secret_sources = {}    # service -> host path of the Web secret mount
        self.auth = {"development": False, "oidc": True}
        self.stopped = []
        self.databases = ["baobei"]
        self.restores = 0
        self.writes = []
        self.leftover_scratch = []
        self.scratch = None         # the restore database while it is being built
        self.on_restore = self.on_write = None
        self.realm_running, self.realm_env, self.secret_source = "crane", False, ""

    def ledger_of(self, sha):
        mig = self.trees[sha] / "services/project-intake-api/migrations"
        return [{"name": f.name, "checksum": direct.digest(f.read_bytes())} for f in sorted(mig.glob("*.sql"))]

    # git for expected_ledger (the simulated commits)
    def source_migrations(self, sha):
        mig = self.trees[sha] / "services/project-intake-api/migrations"
        return ["services/project-intake-api/migrations/" + f.name for f in sorted(mig.glob("*.sql"))]

    def source_blob(self, sha, path):
        return (self.trees[sha] / path).read_bytes()

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


def minimal_privileges():
    roles = {r: {"login": True, "elevated": False, "memberships": 0, "connect": True, "create": False, "temp": False,
                 "other_databases": False, "schema_usage": True, "schema_create": r == "baobei_migrator"}
             for r in direct.APP_ROLES}
    return {"roles": roles, "public_database": 0, "public_schema": 0, "public_relations": 0, "foreign_owned": 0}


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
            # The API refuses to start unless the database trusts its issuer
            # (releases before 00006 have no identity epoch at all).
            epoch_schema = any(e["name"].startswith("00006") for e in self.w.ledger)
            if epoch_schema and self.w.epoch_issuer != direct.REALMS[realm]["issuer"]:
                raise ValueError("docker compose 失败（API 身份纪元不匹配，退出码 1）")
            for service in direct.SERVICES:
                self.w.running[service] = images[service]["id"]
            self.w.realm_running = realm
            self.w.api_state = "running"
            self.w.realm_env = "BAOBEI_IDENTITY_REALM" in (release / "compose.production.yaml").read_text()
            self.w.secret_source = str(secrets / direct.REALMS[realm]["web_secret"])
            self.w.ups.append((realm, images["baobei-api"]["id"]))
            return None
        if args[:2] == ("stop", "baobei-api"):
            self.w.stopped.append("baobei-api")
            self.w.api_state = "exited"
            return None
        if args[:2] == ("ps", "-q"):
            return (args[2] + "-container\n").encode()
        if args[:3] == ("ps", "-a", "-q"):
            return (args[3] + "-container\n").encode()
        raise AssertionError(args)

    def call(self, *args, capture=True, env=None, cwd=None, stdin=None):
        if args[:2] == ("network", "inspect"):   # 2026-11-19 account service network
            net = getattr(self.w, "admin_network", "default")
            if net is None:
                raise ValueError("docker network inspect 失败（不存在）")
            if net == "default":
                net = {"Internal": True, "Driver": "bridge",
                       "Containers": {"kc-container": {}, "baobei-api-container": {}}}
            return json.dumps([net]).encode()
        if args[:2] == ("inspect", "kc-container"):
            return json.dumps([{"Config": {"Image": "quay.io/keycloak/keycloak:26.8.1", "Labels": {}},
                                "NetworkSettings": {"Networks": {"u-dao-baobei-kc-admin": {"Aliases": ["baobei-kc-admin"]}}}}]).encode()
        if args[:2] == ("inspect", "baobei-api-container"):
            return json.dumps([{"Config": {"Image": "baobei-api", "Labels": {
                "com.docker.compose.project": "u-dao-baobei", "com.docker.compose.service": "baobei-api"}},
                "NetworkSettings": {"Networks": {}}}]).encode()
        if args[:2] == ("inspect", "crane-db-container"):
            return json.dumps([{"Config": {"Image": "postgres:18", "Labels": {}},
                                "NetworkSettings": {"Networks": {}}}]).encode()
        if args[0] == "run":                     # the migrator
            self.w.migrations += 1
            self.w.ledger = self.w.ledger_of(NEW)
            return None
        if args[:3] == ("exec", "baobei-web-container", "wget"):
            return json.dumps(self.w.auth).encode()
        if args[:3] == ("exec", "-i", "baobei-postgres-container"):   # pg_restore
            assert args[3] == "pg_restore" and stdin is not None and "--no-owner" in args, args
            self.w.restores += 1
            if self.w.on_restore:
                self.w.on_restore(args[args.index("-d") + 1])
            return b""
        if args[0] == "exec":                    # database checks
            sql = args[-1]
            database = args[args.index("-d") + 1]
            if not sql.startswith("BEGIN READ ONLY;"):
                # Only the disaster-recovery fixture models database writes;
                # anywhere else a write is a defect of the tool.
                assert self.w.on_write is not None, ("unexpected database write", database, sql)
                self.w.writes.append((database, sql))
                self.w.on_write(database, sql)
                return b""
            if "'roles'" not in sql and "FROM pg_database" in sql:
                assert database == "postgres", database
                if "string_agg" in sql:
                    return " ".join(self.w.leftover_scratch).encode()
                name = sql.split("datname = '")[1].split("'")[0]
                return b"1" if name in self.w.databases else b"0"
            if database != "baobei":
                view = self.w.scratch
                assert view is not None and database == view["name"], (database, view)
            else:
                view = {"ledger": self.w.ledger, "epoch": self.w.epoch_issuer, "privileges": self.w.privileges,
                        "counts": self.w.counts}
            if "baobei_migrations" in sql and "json_agg" in sql:
                return json.dumps(view["ledger"]).encode()
            has_epoch = any(e["name"].startswith("00006") for e in view["ledger"])
            if "to_regclass('public.identity_epoch')" in sql:
                return b"t" if has_epoch else b"f"
            if "identity_epoch" in sql:
                # Like PostgreSQL: a missing table is a parse error, not NULL.
                if not has_epoch:
                    raise ValueError("psql 失败（relation identity_epoch does not exist）")
                return ((view["epoch"] if database != "baobei" else self.w.epoch_issuer) or "").encode()
            if "pg_stat_activity" in sql:
                return str(self.w.connections).encode()
            if "'roles'" in sql:
                return json.dumps(view["privileges"]).encode()
            if sql.startswith("BEGIN READ ONLY; SELECT json_build_object("):
                return json.dumps(view["counts"]).encode()
            raise AssertionError(sql)
        assert args[0] == "inspect", args
        container = args[-1]
        if args[2] == "{{.State.Status}}":
            return self.w.api_state.encode()
        if args[2].startswith("[{{json .Image}}"):
            realm = self.w.realm_running
            profile = direct.REALMS[realm]
            env = ["OIDC_ISSUER=" + profile["issuer"], "OIDC_CLIENT_ID=baobei-web",
                   "OIDC_CLIENT_SECRET_FILE=/run/secrets/oidc_client_secret"]
            if self.w.realm_env:
                env.append("BAOBEI_IDENTITY_REALM=" + realm)
            mounts = [{"Destination": "/run/secrets/oidc_client_secret", "Source": self.w.secret_source}]
            return json.dumps([self.w.running["baobei-api"], env, mounts,
                               "running" if self.w.api_state != "exited" else "exited"]).encode()
        if container == "baobei-postgres-container":
            return f"{self.w.pg[0]} {self.w.pg[1]} healthy".encode()
        return (self.w.running[container.removesuffix("-container")] + "\n").encode()


def prepare_old(docker, state, secrets):
    """The currently deployed pre-RC0 release (G5-C-P0): the real files of the
    deployed commit and the manifest exactly as the old tool wrote it (no realm
    fields, old snapshot list, compose_sha256 of the old Compose)."""
    import subprocess
    release = state / "releases" / OLD
    for name in direct.LEGACY_SNAPSHOT:
        body = subprocess.run(["git", "show", f"{OLD}:deploy/baobei/{name}"], cwd=ROOT, check=True,
                              capture_output=True).stdout
        (release / name).parent.mkdir(parents=True, exist_ok=True)
        (release / name).write_bytes(body)
    images = docker.w.build_images(docker, OLD, None, None)
    config = json.loads(docker.compose(release, secrets, images, "config", realm="crane"))
    manifest = {"mode": "direct-local", "source_sha": OLD, "source": direct.SOURCE, "platform": docker.platform,
                "built_at": "2026-11-11T00:00:00+00:00", "images": images, "go_proxy": direct.PUBLIC_GO_PROXIES[0],
                "compose_sha256": direct.digest(direct.canonical_json(config)),
                "snapshot_sha256": direct.hash_files(release, direct.LEGACY_SNAPSHOT),
                "database_contract_sha256": direct.database_contract(docker.w.trees[OLD]), "rollback_target": None}
    direct.atomic_json(release / "manifest.json", manifest)
    direct.atomic_json(state / "active.json", {"source_sha": OLD})


ADMIN = "11111111-1111-4111-8111-111111111111"
KC_SUB = "33333333-3333-4333-8333-333333333333"


def write_secrets(secrets):
    """Each realm has its own Web client secret (CR-08C); values are synthetic."""
    for name in direct.BASE_SECRETS:
        (secrets / name).write_text("synthetic-" + name)
    (secrets / "oidc_client_secret").write_text("synthetic-crane-web")
    (secrets / "baobei_oidc_client_secret").write_text("synthetic-baobei-web")
    (secrets / "account_client_secret").write_text("synthetic-account")


def patched(world, tmp):
    from contextlib import ExitStack
    stack = ExitStack()
    stack.enter_context(patch.object(direct, "verify_source"))
    stack.enter_context(patch.object(direct, "export_source", world.export_source))
    stack.enter_context(patch.object(direct, "build_images", world.build_images))
    stack.enter_context(patch.object(direct, "WEBROOT", tmp))
    stack.enter_context(patch.object(direct.sys, "platform", "linux"))
    stack.enter_context(patch.object(direct, "source_migrations", world.source_migrations))
    stack.enter_context(patch.object(direct, "source_blob", world.source_blob))
    return stack


def tool_result(realm="baobei"):
    return {"ADMIN_ACCOUNT_VERIFIED": "PASS", "ADMIN_LOGIN_VERIFIED": "PASS", "realm": realm,
            "issuer": direct.REALMS[realm]["issuer"],
            "admin": {"staffId": ADMIN, "keycloakSubject": KC_SUB, "businessSubject": "s-1", "problems": []},
            "login": {"sub": KC_SUB, "roles": ["baobei_admin"]}}


def run_preflight(state, rid, secrets, change, web_secret=None):
    """The release tool's own preflight; only the Keycloak side is faked."""
    with patch.object(direct, "execute_preflight_tool", return_value=tool_result()):
        return direct.preflight(state, rid, ADMIN, change, {
            "base": "https://crane.youdaozd.com",
            "web_secret_file": web_secret or secrets / "baobei_oidc_client_secret"}, secrets=secrets)


def realm_switch(test, docker, world, state, secrets, rid, realm, change="CHG-SW"):
    """CR-08A path A as the runbook runs it: realm-switch window, measured
    baseline, precheck + identity switch, start, smoke, complete."""
    direct.record_phase(state, rid, "MAINTENANCE_ENTER", change, kind="realm-switch")
    window = direct.read_window(state)
    test.assertEqual(window["kind"], "realm-switch")
    test.assertNotIn("SCHEMA_UPGRADE", direct.window_phases(window))
    direct.realm_switch_baseline(docker, state, secrets, rid)
    if realm == "crane":
        direct.record_phase(state, rid, "IDENTITY_PRECHECK", "precheck-" + change)
        direct.record_phase(state, rid, "IDENTITY_SWITCH", "switch-" + change)
    else:
        run_preflight(state, rid, secrets, change)
        direct.record_phase(state, rid, "IDENTITY_SWITCH", "switch-" + change)
    # The database still trusts the other realm: refused before Compose.
    attempted = len(world.attempted)
    with test.assertRaisesRegex(ValueError, "身份纪元为空或未指向目标 Realm"):
        direct.apply(docker, state, secrets, rid, "up", None, realm)
    test.assertEqual(len(world.attempted), attempted)
    world.epoch_issuer = direct.REALMS[realm]["issuer"]
    with test.assertRaisesRegex(ValueError, "--confirm-realm"):
        direct.apply(docker, state, secrets, rid, "up", None)
    direct.apply(docker, state, secrets, rid, "up", None, realm)
    window = direct.read_window(state)
    test.assertEqual(direct.next_phase(window), "SMOKE_TEST")
    test.assertEqual(window["phases"][-2]["measured"]["realm"], realm)
    test.assertEqual(window["phases"][-2]["measured"]["auth_config"], "oidc")
    direct.record_phase(state, rid, "SMOKE_TEST", "smoke-" + change)
    window = direct.record_phase(state, rid, "RELEASE_COMPLETE", "done-" + change)
    test.assertEqual(window["status"], "COMPLETE")
    test.assertEqual([e["phase"] for e in window["phases"]], list(direct.REALM_SWITCH_PHASES))
    test.assertIsNone(direct.pointer(state, "pending"))
    return window


class PathATests(unittest.TestCase):
    def test_prepare_both_realms_then_switch_back_to_crane(self):
        with TemporaryDirectory() as tmp:
            tmp = Path(tmp).resolve()
            world = World(tmp)
            state, secrets = tmp / "state", tmp / "secrets"
            state.mkdir(mode=0o700)
            secrets.mkdir()
            write_secrets(secrets)
            docker = FakeDocker(world)
            with patched(world, tmp):
                # 1. Production today: the old release (crane) is active.
                world.epoch_issuer = direct.REALMS["crane"]["issuer"]   # (pre-00006 the old API has no epoch; modelled as trusted)
                prepare_old(docker, state, secrets)
                direct.verify_release(docker, state, secrets, OLD)   # the real deployed release verifies
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
                direct.record_phase(state, baobei_id, "IDENTITY_EPOCH_INIT", "epoch:1:crane")
                run_preflight(state, baobei_id, secrets, "CHG-UP-1")
                direct.record_phase(state, baobei_id, "IDENTITY_SWITCH", "evidence-identity_switch")
                # Starting baobei before the epoch trusts baobei is refused.
                with self.assertRaisesRegex(ValueError, "身份纪元为空或未指向目标 Realm"):
                    direct.apply(docker, state, secrets, baobei_id, "up", None, "baobei")
                world.epoch_issuer = direct.REALMS["baobei"]["issuer"]
                direct.apply(docker, state, secrets, baobei_id, "up", None, "baobei")
                self.assertEqual(direct.pointer(state, "active"), baobei_id)
                self.assertEqual(world.secret_source, str(secrets / "baobei_oidc_client_secret"))
                pg_before, api_new = world.pg, world.running["baobei-api"]
                # CR-08A: the upgrade window ends normally.
                direct.record_phase(state, baobei_id, "SMOKE_TEST", "smoke-1")
                direct.record_phase(state, baobei_id, "RELEASE_COMPLETE", "done-1")
                self.assertEqual(direct.read_window(state)["status"], "COMPLETE")
                # 6. Path A back to crane: its own realm-switch window, no SCHEMA_UPGRADE.
                with self.assertRaisesRegex(ValueError, "维护窗口"):
                    direct.apply(docker, state, secrets, crane_id, "up", None, "crane")   # no window
                realm_switch(self, docker, world, state, secrets, crane_id, "crane")
                # 7. Back on crane, same new images and schema, PostgreSQL untouched.
                self.assertEqual(direct.pointer(state, "active"), crane_id)
                self.assertEqual(world.running["baobei-api"], api_new)
                self.assertEqual(world.pg, pg_before)
                self.assertEqual(world.ups[-1][0], "crane")
                self.assertEqual(world.secret_source, str(secrets / "oidc_client_secret"))
                # Switching to baobei again (same commit) is the same controlled path.
                realm_switch(self, docker, world, state, secrets, baobei_id, "baobei")
                self.assertEqual(direct.pointer(state, "active"), baobei_id)
                self.assertEqual(world.secret_source, str(secrets / "baobei_oidc_client_secret"))
                # The old image on the old schema contract stays refused (path B only).
                with self.assertRaisesRegex(ValueError, "数据库契约不同"):
                    direct.apply(docker, state, secrets, baobei_id, "rollback", None, "crane")
                self.assertEqual(direct.pointer(state, "active"), baobei_id)
                self.assertEqual(world.pg, pg_before)
                self.assertEqual(world.migrations, 1)

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
