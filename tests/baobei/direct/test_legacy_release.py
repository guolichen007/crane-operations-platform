"""G5-C-P0 (P0-LEGACY-01): the deployed pre-RC0 releases must verify with the new
tool, from their real files, without mocking config_hash.

The old release is built from the actual commits (git show 253b4a8…/7b95580…:
deploy/baobei/*) with the old tool's own manifest code path (snapshot names,
snapshot_sha256, compose_sha256 = sha256 of `docker compose config` for the old
files, database_contract_sha256). The Compose rendering here is a YAML merge of
the snapshot files (same as compose would produce for these inputs); run.py in CI
repeats the check with the real `docker compose config`.
"""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[3]
ENTRY = ROOT / "deploy/baobei/direct-deploy.py"
SPEC = importlib.util.spec_from_file_location("direct_deploy_legacy", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)

DEPLOYED = "7b9558084d73f5210cdd08e8240883ecc1659de8"
REVIEWED = "253b4a8f8e612c2380355b83e8a790e8c9cd7d8a"
NEW = "c" * 40
OLD_SNAPSHOT = ("compose.production.yaml", "compose.direct.yaml", "nginx.production.conf",
                "nginx.proxy.production.conf", "db/initialize.sql", "db/staff-role.sql")


def git_show(sha, path):
    try:
        return subprocess.run(["git", "show", f"{sha}:{path}"], cwd=ROOT, check=True, capture_output=True).stdout
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None


def merge(a, b):
    if isinstance(a, dict) and isinstance(b, dict):
        out = dict(a)
        for k, v in b.items():
            out[k] = merge(a[k], v) if k in a and isinstance(v, dict) else v
        return out
    return b


def render(release, secrets, images, realm):
    """`docker compose config --no-path-resolution --format json` for the release files."""
    import yaml
    files = ["compose.production.yaml", "compose.direct.yaml"]
    overlay = direct.REALMS[realm]["overlay"]
    if overlay:
        files.append(overlay)
    config = {}
    for name in files:
        text = (release / name).read_text(encoding="utf-8")
        text = text.replace("${BAOBEI_SECRETS_DIR:?External secret directory}", str(secrets))
        text = text.replace("${BAOBEI_WEB_IMAGE:?Pin the approved web image digest}", images["baobei-web"]["ref"])
        text = text.replace("${BAOBEI_API_IMAGE:?Pin the approved api image digest}", images["baobei-api"]["ref"])
        config = merge(config, yaml.safe_load(text))
    return config


class FakeDocker:
    platform = "linux/amd64"

    def __init__(self, images):
        self.images = images

    def inspect(self, ref, source):
        for image in self.images.values():
            if image["ref"] == ref:
                return dict(image)
        raise ValueError("本地镜像不存在")

    def compose(self, release, secrets, images, *args, capture=True, realm="crane"):
        assert args[0] == "config", args
        return json.dumps(render(release, secrets, images, realm)).encode()


def images_for(sha, char):
    return {s: {"ref": direct.local_ref(s, sha), "id": "sha256:" + char * 64, "created_at": "2026-11-11T00:00:00Z"}
            for s in direct.SERVICES}


def old_release(state, secrets, sha):
    """The release directory and manifest exactly as the pre-RC0 tool wrote them."""
    files = {name: git_show(sha, "deploy/baobei/" + name) for name in OLD_SNAPSHOT}
    if any(v is None for v in files.values()):
        return None
    release = state / "releases" / sha
    for name, body in files.items():
        (release / name).parent.mkdir(parents=True, exist_ok=True)
        (release / name).write_bytes(body)
    images = images_for(sha, "a")
    config = render(release, secrets, images, "crane")   # old config_hash: no identity check
    manifest = {
        "mode": "direct-local", "source_sha": sha, "source": direct.SOURCE, "platform": "linux/amd64",
        "built_at": "2026-11-11T00:00:00+00:00", "images": images, "go_proxy": direct.PUBLIC_GO_PROXIES[0],
        "compose_sha256": direct.digest(direct.canonical_json(config)),
        "snapshot_sha256": direct.hash_files(release, OLD_SNAPSHOT),
        "database_contract_sha256": "contract-00005", "rollback_target": None,
    }
    direct.atomic_json(release / "manifest.json", manifest)
    return release, manifest, images


def new_release(state, secrets, realm, contract="contract-00008"):
    rid = direct.release_id(NEW, realm)
    release = state / "releases" / rid
    for name in direct.SNAPSHOT:
        (release / name).parent.mkdir(parents=True, exist_ok=True)
        (release / name).write_bytes((ROOT / "deploy/baobei" / name).read_bytes())
    images = images_for(NEW, "c")
    config = render(release, secrets, images, realm)
    manifest = {
        "mode": "direct-local", "source_sha": NEW, "source": direct.SOURCE, "platform": "linux/amd64",
        "built_at": "x", "images": images, "go_proxy": direct.PUBLIC_GO_PROXIES[0],
        "identity_realm": realm, "identity_issuer": direct.REALMS[realm]["issuer"],
        "compose_sha256": direct.digest(direct.canonical_json(config)),
        "snapshot_sha256": direct.hash_files(release, direct.SNAPSHOT),
        "database_contract_sha256": contract, "rollback_target": DEPLOYED, "tools_sha256": "t",
        "web_secret": direct.REALMS[realm]["web_secret"],   # recorded by prepare since CR-08C
    }
    direct.atomic_json(release / "manifest.json", manifest)
    return rid, release, manifest, images


class Env:
    def __init__(self, tmp):
        self.tmp = Path(tmp).resolve()
        self.state = self.tmp / "state"
        self.state.mkdir(mode=0o700)
        self.secrets = self.tmp / "secrets"
        self.secrets.mkdir()


class LegacyReleaseTests(unittest.TestCase):
    def setUp(self):
        if git_show(DEPLOYED, "deploy/baobei/compose.production.yaml") is None:
            self.skipTest("NOT_RUN: git history of the deployed commits is not available")

    def test_real_old_compose_has_no_realm_field(self):
        for sha in (DEPLOYED, REVIEWED):
            text = git_show(sha, "deploy/baobei/compose.production.yaml").decode()
            self.assertNotIn("BAOBEI_IDENTITY_REALM", text)
            self.assertIn("OIDC_ISSUER: https://crane.youdaozd.com/realms/crane", text)

    def test_unmodified_old_release_verifies_without_mocking_config_hash(self):
        for sha in (DEPLOYED, REVIEWED):
            with self.subTest(sha=sha[:7]), TemporaryDirectory() as tmp:
                e = Env(tmp)
                release, manifest, images = old_release(e.state, e.secrets, sha)
                raw = (release / "manifest.json").read_bytes()
                got_release, got = direct.verify_release(FakeDocker(images), e.state, e.secrets, sha)
                self.assertEqual(got, manifest)
                self.assertEqual((release / "manifest.json").read_bytes(), raw, "old manifest must not be modified")

    def test_tampered_old_release_is_refused(self):
        def compose_issuer(release, manifest, images):
            p = release / "compose.production.yaml"
            p.write_text(p.read_text().replace("realms/crane", "realms/evil"))

        def compose_issuer_rehashed(release, manifest, images):
            compose_issuer(release, manifest, images)
            manifest["snapshot_sha256"] = direct.hash_files(release, OLD_SNAPSHOT)

        def compose_realm_added(release, manifest, images):
            p = release / "compose.production.yaml"
            p.write_text(p.read_text().replace("      OIDC_ISSUER:", "      BAOBEI_IDENTITY_REALM: baobei\n      OIDC_ISSUER:"))
            manifest["snapshot_sha256"] = direct.hash_files(release, OLD_SNAPSHOT)

        def image(release, manifest, images):
            manifest["images"]["baobei-api"]["id"] = "sha256:" + "f" * 64

        def config_hash(release, manifest, images):
            manifest["compose_sha256"] = "0" * 64

        def extra_key(release, manifest, images):
            manifest["note"] = "edited"

        def other_commit(release, manifest, images):
            manifest["source_sha"] = "d" * 40

        for name, mutate in (("compose file edited", compose_issuer), ("compose edited + snapshot rehashed", compose_issuer_rehashed),
                             ("realm field added to old compose", compose_realm_added), ("image id", image),
                             ("compose hash", config_hash), ("unknown manifest key", extra_key), ("source", other_commit)):
            with self.subTest(name=name), TemporaryDirectory() as tmp:
                e = Env(tmp)
                release, manifest, images = old_release(e.state, e.secrets, DEPLOYED)
                docker = FakeDocker(copy.deepcopy(images))
                m = copy.deepcopy(manifest)
                mutate(release, m, images)
                direct.atomic_json(release / "manifest.json", m)
                with self.assertRaises(ValueError):
                    direct.verify_release(docker, e.state, e.secrets, DEPLOYED)

    def test_unapproved_commit_in_old_format_is_refused(self):
        with TemporaryDirectory() as tmp:
            e = Env(tmp)
            release, manifest, images = old_release(e.state, e.secrets, DEPLOYED)
            other = "e" * 40
            (e.state / "releases" / DEPLOYED).rename(e.state / "releases" / other)
            m = {**manifest, "source_sha": other, "images": images_for(other, "a")}
            direct.atomic_json(e.state / "releases" / other / "manifest.json", m)
            with self.assertRaisesRegex(ValueError, "旧版"):
                direct.verify_release(FakeDocker(m["images"]), e.state, e.secrets, other)

    def test_old_release_as_active_baseline_gates_the_incompatible_upgrade(self):
        with TemporaryDirectory() as tmp:
            e = Env(tmp)
            _, old_manifest, old_images = old_release(e.state, e.secrets, DEPLOYED)
            rid, _, new_manifest, new_images = new_release(e.state, e.secrets, "crane")
            docker = FakeDocker({**{"o" + k: v for k, v in old_images.items()}, **new_images})
            self.assertTrue(direct.incompatible(docker, e.state, e.secrets, new_manifest, DEPLOYED))
            direct.atomic_json(e.state / "active.json", {"source_sha": DEPLOYED})
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "start") as start:
                with self.assertRaisesRegex(ValueError, "维护窗口"):
                    direct.apply(docker, e.state, e.secrets, rid, "up", None)
                with self.assertRaisesRegex(ValueError, "维护窗口"):
                    direct.apply(docker, e.state, e.secrets, rid, "migrate", e.secrets / "migrator")
                start.assert_not_called()
            # Rolling back from the new release to the old one across contracts stays refused.
            direct.atomic_json(e.state / "active.json", {"source_sha": rid})
            with patch.object(direct.sys, "platform", "linux"), patch.object(direct, "check_mounts"), \
                 patch.object(direct, "start") as start:
                with self.assertRaisesRegex(ValueError, "数据库契约不同"):
                    direct.apply(docker, e.state, e.secrets, rid, "rollback", None)
                start.assert_not_called()


class NewReleaseFailClosedTests(unittest.TestCase):
    def test_new_release_verifies(self):
        for realm in ("crane", "baobei"):
            with self.subTest(realm=realm), TemporaryDirectory() as tmp:
                e = Env(tmp)
                rid, _, manifest, images = new_release(e.state, e.secrets, realm)
                direct.verify_release(FakeDocker(images), e.state, e.secrets, rid)

    def test_new_release_without_realm_or_disguised_as_legacy_is_refused(self):
        for realm in ("crane", "baobei"):
            for drop in (("identity_realm",), ("identity_issuer",), ("identity_realm", "identity_issuer"),
                         ("identity_realm", "identity_issuer", "tools_sha256")):
                with self.subTest(realm=realm, drop=drop), TemporaryDirectory() as tmp:
                    e = Env(tmp)
                    rid, release, manifest, images = new_release(e.state, e.secrets, realm)
                    m = {k: v for k, v in manifest.items() if k not in drop}
                    direct.atomic_json(release / "manifest.json", m)
                    with self.assertRaises(ValueError):
                        direct.verify_release(FakeDocker(images), e.state, e.secrets, rid)

    def test_wrong_issuer_realm_or_missing_account_secret_is_refused(self):
        cases = {
            "issuer": lambda m: m.update(identity_issuer="https://crane.youdaozd.com/realms/crane"),
            "realm": lambda m: m.update(identity_realm="crane"),
        }
        for name, mutate in cases.items():
            with self.subTest(name=name), TemporaryDirectory() as tmp:
                e = Env(tmp)
                rid, release, manifest, images = new_release(e.state, e.secrets, "baobei")
                mutate(manifest)
                direct.atomic_json(release / "manifest.json", manifest)
                with self.assertRaises(ValueError):
                    direct.verify_release(FakeDocker(images), e.state, e.secrets, rid)
        with TemporaryDirectory() as tmp:
            e = Env(tmp)
            rid, release, manifest, images = new_release(e.state, e.secrets, "baobei")
            overlay = release / "compose.realm-baobei.yaml"
            overlay.write_text(overlay.read_text().replace("      BAOBEI_ACCOUNT_CLIENT_SECRET_FILE: /run/secrets/account_client_secret\n", ""))
            manifest["snapshot_sha256"] = direct.hash_files(release, direct.SNAPSHOT)
            direct.atomic_json(release / "manifest.json", manifest)
            with self.assertRaisesRegex(ValueError, "账号服务"):
                direct.verify_release(FakeDocker(images), e.state, e.secrets, rid)
        # An old-format crane config cannot pass as a current crane release.
        with TemporaryDirectory() as tmp:
            e = Env(tmp)
            rid, release, manifest, images = new_release(e.state, e.secrets, "crane")
            p = release / "compose.production.yaml"
            p.write_text(p.read_text().replace("      BAOBEI_IDENTITY_REALM: crane\n", ""))
            manifest["snapshot_sha256"] = direct.hash_files(release, direct.SNAPSHOT)
            direct.atomic_json(release / "manifest.json", manifest)
            with self.assertRaisesRegex(ValueError, "身份配置"):
                direct.verify_release(FakeDocker(images), e.state, e.secrets, rid)


if __name__ == "__main__":
    unittest.main()
