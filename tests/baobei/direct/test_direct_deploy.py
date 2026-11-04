import importlib.util
from io import BytesIO
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import Mock, patch
from zipfile import ZipFile

ENTRY = Path(__file__).resolve().parents[3] / "deploy/baobei/direct-deploy.py"
SPEC = importlib.util.spec_from_file_location("direct_deploy", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)
CURRENT, PREVIOUS = "a" * 40, "b" * 40


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
