import importlib.util
from io import BytesIO
from pathlib import Path
from tempfile import TemporaryDirectory
import types
import unittest
from unittest.mock import Mock, patch
from zipfile import ZipFile

ENTRY = Path(__file__).resolve().parents[3] / "deploy/baobei/direct-deploy.py"
SPEC = importlib.util.spec_from_file_location("direct_deploy", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)
CURRENT, PREVIOUS = "a" * 40, "b" * 40
APPS = {"baobei-api", "baobei-web"}
MANAGED = APPS | {"baobei-postgres"}


def load_variant(old, new):
    """Load direct-deploy.py with one textual mutation (for T5 mutation checks)."""
    source = ENTRY.read_text(encoding="utf-8")
    if old not in source:
        raise AssertionError("mutation anchor missing: " + old)
    module = types.ModuleType("direct_deploy_mutant")
    module.__file__ = str(ENTRY)
    exec(compile(source.replace(old, new, 1), str(ENTRY), "exec"), module.__dict__)
    return module


def image_id(char):
    return "sha256:" + char * 64


class FakeDocker:
    """Models Compose: reconciling baobei-postgres (directly or as a dependency) recreates it."""

    def __init__(self, postgres="healthy", images=None):
        self.postgres = postgres
        self.pg_generation = 1
        self.images = dict(images or {"baobei-api": image_id("c"), "baobei-web": image_id("d")})
        self.ups = []
        self.inspected = []

    def compose(self, release, secrets, images, *args, capture=True):
        if args[0] == "up":
            self.ups.append(args)
            targets = MANAGED.intersection(args)
            if "baobei-postgres" in targets or "--no-deps" not in args:
                self.postgres, self.pg_generation = "healthy", self.pg_generation + 1
            for service in targets & APPS:
                self.images[service] = images[service]["id"]
            return None
        if args[:2] == ("ps", "-q"):
            service = args[2]
            if service == "baobei-postgres":
                return b"" if self.postgres is None else b"pg-container\n"
            return (service + "-container\n").encode()
        raise AssertionError(args)

    def call(self, *args, capture=True, env=None, cwd=None):
        assert args[0] == "inspect"
        container = args[-1]
        self.inspected.append(container)
        if container == "pg-container":
            return f"pg-{self.pg_generation} 2026-11-11T00:00:0{self.pg_generation}Z {self.postgres}".encode()
        return (self.images[container.removesuffix("-container")] + "\n").encode()


def manifest(contract="same", target=PREVIOUS, char=("e", "f")):
    return {"rollback_target": target, "database_contract_sha256": contract,
            "images": {"baobei-api": {"ref": "api", "id": image_id(char[0])},
                       "baobei-web": {"ref": "web", "id": image_id(char[1])}}}


# Reusable checks; T5 runs them against mutated copies of direct-deploy.py.
def check_app_up_scope(m):
    docker, candidate = FakeDocker(), manifest()
    m.start(docker, Path("/r"), Path("/s"), candidate)
    assert len(docker.ups) == 1, docker.ups
    args = docker.ups[0]
    assert "--no-deps" in args and "--no-build" in args and "--wait" in args
    assert args[args.index("--pull") + 1] == "never"
    assert "--no-recreate" not in args
    assert MANAGED.intersection(args) == APPS, args
    assert args[-2:] == ("baobei-api", "baobei-web")
    assert {"baobei-api-container", "baobei-web-container"} <= set(docker.inspected)
    assert docker.pg_generation == 1


def check_rollback_contract_guard(m):
    with TemporaryDirectory() as tmp:
        state = Path(tmp).resolve()
        m.atomic_json(state / "active.json", {"source_sha": CURRENT})
        releases = {CURRENT: (state / CURRENT, manifest("new")),
                    PREVIOUS: (state / PREVIOUS, manifest("old", None, ("1", "2")))}
        docker = FakeDocker()
        with patch.object(m.sys, "platform", "linux"), patch.object(m, "check_mounts"), \
             patch.object(m, "verify_release", side_effect=lambda d, s, k, sha: releases[sha]):
            try:
                m.apply(docker, state, state, CURRENT, "rollback", None)
            except ValueError as error:
                assert "数据库契约不同" in str(error)
            else:
                raise AssertionError("rollback across database contracts was accepted")
        assert docker.ups == [] and m.pointer(state, "active") == CURRENT
        assert m.pointer(state, "pending") is None


class DirectDeployTests(unittest.TestCase):
    def test_source_gate(self):
        with patch.object(direct, "git", side_effect=[CURRENT.encode(), b"", direct.SOURCE.encode(), b""]) as git:
            direct.verify_source(CURRENT)
            git.assert_called_with("merge-base", "--is-ancestor", CURRENT, direct.PRODUCT_REF)
        for source, replies in [
            ("latest", []), (CURRENT, [PREVIOUS.encode()]),
            (CURRENT, [CURRENT.encode(), b" M tracked"]),
            (CURRENT, [CURRENT.encode(), b"", b"https://example.test/fork"]),
            (CURRENT, [CURRENT.encode(), b"", direct.SOURCE.encode(), ValueError("off product branch")]),
        ]:
            with self.subTest(source=source, replies=replies), patch.object(direct, "git", side_effect=replies):
                with self.assertRaises(ValueError):
                    direct.verify_source(source)

    def test_local_inspect_rejects_wrong_source_revision_architecture_and_tag(self):
        docker = object.__new__(direct.Docker)
        docker.platform = "linux/amd64"
        ref = direct.local_ref("baobei-web", CURRENT)
        labels = {"org.opencontainers.image.source": direct.SOURCE,
                  "org.opencontainers.image.revision": CURRENT}
        image = {"Id": "sha256:" + "a" * 64, "RepoTags": [ref], "Created": "synthetic",
                 "Os": "linux", "Architecture": "amd64", "Config": {"Labels": labels}}
        docker.call = Mock(return_value=direct.canonical_json([image]))
        self.assertEqual(docker.inspect(ref, CURRENT)["id"], image["Id"])
        for changes in [{"Architecture": "arm64"}, {"RepoTags": ["baobei-local/baobei-web:latest"]},
                        {"Config": {"Labels": {**labels, "org.opencontainers.image.source": "other"}}},
                        {"Config": {"Labels": {**labels, "org.opencontainers.image.revision": PREVIOUS}}}]:
            docker.call.return_value = direct.canonical_json([{**image, **changes}])
            with self.assertRaises(ValueError):
                docker.inspect(ref, CURRENT)

    def test_build_inputs_exclude_tracked_secret_paths(self):
        web = "apps/project-intake-web/"
        api = "services/project-intake-api/"
        archive = BytesIO()
        with ZipFile(archive, "w") as z:
            z.writestr(web + "src/main.ts", "synthetic")
        with TemporaryDirectory() as tmp, patch.object(direct, "git", side_effect=[
            "\0".join(web + p for p in ("src/main.ts", ".env", "src/.env.ts", "src/oidc_client_secret")).encode(),
            "\0".join(api + p for p in ("go.mod", "runtime_database_url", "secret.pem")).encode(),
            archive.getvalue(),
        ]) as git:
            direct.export_source(CURRENT, Path(tmp))
            paths = git.call_args.args[4:]
            self.assertIn(web + "src/main.ts", paths)
            self.assertIn(api + "go.mod", paths)
            self.assertFalse(any(".env" in p or "secret" in p or "database_url" in p for p in paths))

    def test_credential_bearing_go_proxy_is_refused_before_build(self):
        with patch.object(direct, "verify_source"), patch.dict(direct.os.environ, {
            "BAOBEI_GOPROXY": "https://synthetic:synthetic@example.test",
        }), patch.object(direct, "build_images") as build:
            with self.assertRaises(ValueError):
                direct.prepare(Mock(), None, None, CURRENT)
            build.assert_not_called()

    def test_missing_mount_is_refused_before_docker(self):
        with TemporaryDirectory() as tmp:
            secrets = Path(tmp).resolve()
            docker = Mock()
            with patch.object(direct.sys, "platform", "linux"), \
                 patch.object(direct, "verify_release", return_value=(secrets, {})), \
                 patch.object(direct, "WEBROOT", secrets / "absent-webroot"):
                with self.assertRaises(ValueError):
                    direct.apply(docker, secrets, secrets, CURRENT, "db", None)
                self.assertFalse((secrets / "db_password").exists())
                for name in direct.SECRET_NAMES:
                    (secrets / name).touch()
                with self.assertRaises(ValueError):
                    direct.apply(docker, secrets, secrets, CURRENT, "up", None)
            self.assertFalse((secrets / "absent-webroot").exists())
            self.assertIsNone(direct.pointer(secrets, "pending"))
            self.assertEqual(docker.mock_calls, [])

    def test_failed_health_keeps_old_active_and_allows_compatible_rollback(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            direct.atomic_json(state / "active.json", {"source_sha": PREVIOUS})
            candidate = {"rollback_target": PREVIOUS, "database_contract_sha256": "same", "images": {}}
            old = {"rollback_target": None, "database_contract_sha256": "different", "images": {}}
            releases = {CURRENT: (state / CURRENT, candidate), PREVIOUS: (state / PREVIOUS, old)}
            docker = Mock()
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "verify_release", side_effect=lambda d, s, k, sha: releases[sha]), \
                 patch.object(direct, "start", side_effect=[ValueError("health failed"), None]) as start:
                with self.assertRaisesRegex(ValueError, "health failed"):
                    direct.apply(docker, state, state, CURRENT, "up", None)
                self.assertEqual(direct.pointer(state, "active"), PREVIOUS)
                self.assertEqual(direct.pointer(state, "pending"), CURRENT)
                with self.assertRaisesRegex(ValueError, "数据库契约不同"):
                    direct.apply(docker, state, state, CURRENT, "rollback", None)
                old["database_contract_sha256"] = "same"
                direct.apply(docker, state, state, CURRENT, "rollback", None)
                start.assert_called_with(docker, state / PREVIOUS, state, old)
            self.assertEqual(direct.pointer(state, "active"), PREVIOUS)
            self.assertIsNone(direct.pointer(state, "pending"))

    # T1：升级命令只协调 Web/API，并在切换后核对真实镜像 ID。
    def test_t1_up_targets_only_web_and_api(self):
        check_app_up_scope(direct)

    def test_t1_running_image_mismatch_is_refused(self):
        docker = FakeDocker()
        docker.compose = Mock(wraps=docker.compose)
        original = FakeDocker.compose

        def drift(release, secrets, images, *args, capture=True):
            result = original(docker, release, secrets, images, *args, capture=capture)
            if args[0] == "up":
                docker.images["baobei-web"] = image_id("9")
            return result
        docker.compose = drift
        with self.assertRaisesRegex(ValueError, "镜像 ID 与部署清单不匹配"):
            direct.start(docker, Path("/r"), Path("/s"), manifest())

    # T2：首装仍由 db 显式启动 PostgreSQL；up 不补建、不启动数据库。
    def test_t2_db_command_only_targets_postgres(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            docker = FakeDocker(postgres=None)
            first = manifest(target=None)
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "verify_release", return_value=(state / CURRENT, first)):
                direct.apply(docker, state, state, CURRENT, "db", None)
            self.assertEqual(len(docker.ups), 1)
            self.assertEqual(MANAGED.intersection(docker.ups[0]), {"baobei-postgres"})
            self.assertEqual(docker.ups[0][-1], "baobei-postgres")
            self.assertNotIn("--no-deps", docker.ups[0])
            self.assertIsNone(direct.pointer(state, "active"))

    def test_t2_up_refuses_absent_or_unhealthy_postgres(self):
        for postgres in (None, "unhealthy", "starting", ""):
            with self.subTest(postgres=postgres), TemporaryDirectory() as tmp:
                state = Path(tmp).resolve()
                direct.atomic_json(state / "active.json", {"source_sha": PREVIOUS})
                docker = FakeDocker(postgres=postgres)
                with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                     patch.object(direct, "verify_release", return_value=(state / CURRENT, manifest())):
                    with self.assertRaisesRegex(ValueError, "PostgreSQL"):
                        direct.apply(docker, state, state, CURRENT, "up", None)
                self.assertEqual(docker.ups, [])
                self.assertEqual(direct.pointer(state, "active"), PREVIOUS)

    def test_t2_postgres_changed_during_switch_is_refused(self):
        docker = FakeDocker()
        original = FakeDocker.compose

        def recreate(release, secrets, images, *args, capture=True):
            result = original(docker, release, secrets, images, *args, capture=capture)
            if args[0] == "up":
                docker.pg_generation += 1
            return result
        docker.compose = recreate
        with self.assertRaisesRegex(ValueError, "PostgreSQL 容器在应用切换期间被改变"):
            direct.start(docker, Path("/r"), Path("/s"), manifest())

    # T4：回滚与异常。
    def test_t4_compatible_rollback_only_switches_web_and_api(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            direct.atomic_json(state / "active.json", {"source_sha": CURRENT})
            previous = manifest("same", None, ("1", "2"))
            releases = {CURRENT: (state / CURRENT, manifest("same")), PREVIOUS: (state / PREVIOUS, previous)}
            docker = FakeDocker()
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "verify_release", side_effect=lambda d, s, k, sha: releases[sha]):
                direct.apply(docker, state, state, CURRENT, "rollback", None)
            self.assertEqual(len(docker.ups), 1)
            self.assertEqual(MANAGED.intersection(docker.ups[0]), APPS)
            self.assertIn("--no-deps", docker.ups[0])
            self.assertEqual(docker.pg_generation, 1)
            self.assertEqual(docker.images, {s: i["id"] for s, i in previous["images"].items()})
            self.assertEqual(direct.pointer(state, "active"), PREVIOUS)
            self.assertIsNone(direct.pointer(state, "pending"))

    def test_t4_rollback_across_database_contracts_is_refused(self):
        check_rollback_contract_guard(direct)

    def test_t4_unhealthy_apps_do_not_advance_active(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            direct.atomic_json(state / "active.json", {"source_sha": PREVIOUS})
            docker = FakeDocker()
            original = FakeDocker.compose

            def unhealthy(release, secrets, images, *args, capture=True):
                if args[0] == "up":
                    raise ValueError("docker compose 失败（退出码 1）")
                return original(docker, release, secrets, images, *args, capture=capture)
            docker.compose = unhealthy
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "verify_release", return_value=(state / CURRENT, manifest())):
                with self.assertRaises(ValueError):
                    direct.apply(docker, state, state, CURRENT, "up", None)
            self.assertEqual(direct.pointer(state, "active"), PREVIOUS)
            self.assertEqual(direct.pointer(state, "pending"), CURRENT)

    def test_t4_manifest_mismatch_stops_before_compose(self):
        with TemporaryDirectory() as tmp:
            state = Path(tmp).resolve()
            direct.atomic_json(state / "active.json", {"source_sha": PREVIOUS})
            docker = FakeDocker()
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "verify_release", side_effect=ValueError("Compose 配置哈希不匹配")):
                with self.assertRaises(ValueError):
                    direct.apply(docker, state, state, CURRENT, "up", None)
            self.assertEqual(docker.ups, [])
            self.assertIsNone(direct.pointer(state, "pending"))

    # T5：变异必须被上述检查发现。
    def test_t5_mutations_are_detected(self):
        mutations = {
            "drop --no-deps": ('"never", "--no-deps",', '"never",', check_app_up_scope),
            "drop api target": ('"180", "baobei-api", "baobei-web")', '"180", "baobei-web")', check_app_up_scope),
            "whole project up": ('"180", "baobei-api", "baobei-web")', '"180")', check_app_up_scope),
            "relax contract guard": (
                'if manifest["database_contract_sha256"] != previous["database_contract_sha256"]:',
                'if False:', check_rollback_contract_guard),
        }
        for name, (old, new, check) in mutations.items():
            with self.subTest(mutation=name):
                mutant = load_variant(old, new)
                with self.assertRaises((AssertionError, ValueError)):
                    check(mutant)
        check_app_up_scope(load_variant("", ""))


if __name__ == "__main__":
    unittest.main()
