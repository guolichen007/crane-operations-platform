"""Exercise real local builds and Compose without connecting to production."""
from pathlib import Path
import importlib.util
import json
import secrets
import sys
from tempfile import TemporaryDirectory

ENTRY = Path(__file__).resolve().parents[3] / "deploy/baobei/direct-deploy.py"
SPEC = importlib.util.spec_from_file_location("direct_deploy", ENTRY)
direct = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(direct)


def test():
    source = direct.git("rev-parse", "HEAD").decode().strip()
    docker = direct.Docker()
    with TemporaryDirectory(prefix="baobei-direct-check-") as tmp:
        work = Path(tmp).resolve()
        state = work / "state"
        state.mkdir(mode=0o700)
        secrets_dir = work / "external"
        secrets_dir.mkdir()
        (secrets_dir / "runtime_database_url").write_text("synthetic-outside-build")
        with direct.deployment_lock(state):
            release = direct.prepare(docker, state, secrets_dir, source)
        release, manifest = direct.verify_release(docker, state, secrets_dir, source)
        assert manifest["mode"] == "direct-local" and manifest["rollback_target"] is None
        assert manifest["source_sha"] == source
        for image in manifest["images"].values():
            assert direct.IMAGE_ID.fullmatch(image["id"])
            assert image["ref"].startswith("baobei-local/") and image["ref"].endswith(":" + source)
            assert "digest" not in image
        config_args = ("config", "--no-path-resolution", "--format", "json")
        rendered = json.loads(docker.compose(release, secrets_dir, manifest["images"], *config_args))
        baseline = json.loads(docker.call(
            "compose", "--project-name", "u-dao-baobei", "-f", "compose.production.yaml",
            *config_args, cwd=release,
            env={"COMPOSE_DISABLE_ENV_FILE": "1", "BAOBEI_SECRETS_DIR": str(secrets_dir),
                 "BAOBEI_WEB_IMAGE": "ghcr.io/guolichen007/baobei-web@sha256:" + "a"*64,
                 "BAOBEI_API_IMAGE": "ghcr.io/guolichen007/baobei-api@sha256:" + "b"*64}))
        for name in direct.SERVICES:
            service = rendered["services"][name]
            assert service.pop("pull_policy") == "never"
            assert service["image"] == manifest["images"][name]["ref"]
            service["image"] = baseline["services"][name]["image"]
        assert rendered == baseline
        for service in rendered["services"].values():
            assert not service.get("ports") and not service.get("build")
        assert rendered["services"]["baobei-api"]["environment"]["LISTEN_ADDR"] == "0.0.0.0:8088"
        assert "8080/health/ready" in rendered["services"]["baobei-web"]["healthcheck"]["test"][-1]

        # Exercise Docker Compose's real missing-mount behavior in a dedicated fixture.
        # Rendering may omit the false default. Confirm the persisted template uses it,
        # and reproduce the precise bind for all three targets using absent temp paths.
        template = (release / "compose.production.yaml").read_text()
        assert template.count("create_host_path: false") == 3
        volumes = [rendered["services"]["baobei-web"]["volumes"][0],
                   *[v for v in rendered["services"]["baobei-postgres"]["volumes"] if v["type"] == "bind"]]
        assert len(volumes) == 3
        assert all(v["type"] == "bind" and v["read_only"] for v in volumes)
        print("DIRECT_LOCAL_BUILD=PASS DIRECT_MANIFEST=PASS NO_GHCR=PASS")
        if sys.platform != "linux":
            print("BIND_ENGINE_MISSING_DENIED=NOT_RUN_NON_LINUX")
            return
        project = "baobei-direct-test-" + secrets.token_hex(4)
        compose_file = work / "mount-test.json"
        for index, volume in enumerate(volumes):
            missing = work / f"missing-{index}"
            fixture = {"services": {"probe": {
                "image": manifest["images"]["baobei-web"]["ref"], "pull_policy": "never",
                "network_mode": "none", "entrypoint": ["/bin/true"], "volumes": [
                    {**volume, "source": str(missing), "bind": {"create_host_path": False}},
                ],
            }}}
            compose_file.write_text(json.dumps(fixture))
            args = ("compose", "--project-name", project, "-f", str(compose_file))
            try:
                docker.call(*args, "up", "--no-build", "--pull", "never",
                            "--abort-on-container-exit", "--exit-code-from", "probe")
            except ValueError:
                assert not missing.exists(), "Compose unexpectedly created the mount source"
            else:
                raise AssertionError("Compose accepted a missing bind mount")
            finally:
                docker.call(*args, "down", "--remove-orphans")
            # Positive control uses the same service after provisioning the expected path.
            if index == 0:
                missing.mkdir()
            else:
                missing.touch()
            try:
                docker.call(*args, "up", "--no-build", "--pull", "never",
                            "--abort-on-container-exit", "--exit-code-from", "probe")
            finally:
                docker.call(*args, "down", "--remove-orphans")
        print("BIND_ENGINE_MISSING_DENIED=PASS")


if __name__ == "__main__":
    test()
