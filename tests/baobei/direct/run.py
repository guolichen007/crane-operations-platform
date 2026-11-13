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
        # G5-RC0: same commit prepared explicitly for the dedicated realm. Real
        # Compose must render the baobei issuer, client and the account-service
        # secret mount; the images are reused, the release id and hash differ.
        assert manifest["identity_realm"] == "crane"
        (secrets_dir / "account_client_secret").write_text("synthetic-outside-build")
        with direct.deployment_lock(state):
            direct.prepare(docker, state, secrets_dir, source, "baobei")
        rid = direct.release_id(source, "baobei")
        brelease, bmanifest = direct.verify_release(docker, state, secrets_dir, rid)
        assert bmanifest["identity_realm"] == "baobei" and bmanifest["images"] == manifest["images"]
        assert bmanifest["compose_sha256"] != manifest["compose_sha256"]
        brendered = json.loads(docker.compose(brelease, secrets_dir, bmanifest["images"], *config_args, realm="baobei"))
        api = brendered["services"]["baobei-api"]
        assert api["environment"]["OIDC_ISSUER"] == "https://crane.youdaozd.com/realms/baobei"
        assert api["environment"]["BAOBEI_ACCOUNT_CLIENT_ID"] == "baobei-account-service"
        assert any((s["source"] if isinstance(s, dict) else s) == "account_client_secret" for s in api["secrets"])
        assert brendered["secrets"]["account_client_secret"]["file"] == str(secrets_dir / "account_client_secret")
        assert "BAOBEI_ACCOUNT_CLIENT_ID" not in rendered["services"]["baobei-api"]["environment"]
        try:
            direct.verify_release(docker, state, secrets_dir, source + "+crane")
        except ValueError:
            pass
        else:
            raise AssertionError("unknown release id accepted")
        print("DIRECT_REALM_CRANE_DEFAULT=PASS DIRECT_REALM_BAOBEI_WIRED=PASS")
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
        postgres_not_recreated(docker, work, manifest, rendered["services"]["baobei-postgres"]["image"])


FIXTURE = """name: {project}
services:
  baobei-postgres:
    image: {postgres}
    environment:
      POSTGRES_PASSWORD: {password}
    volumes:
      - pgdata:/var/lib/postgresql
      - type: bind
        source: ./db/initialize.sql
        target: /docker-entrypoint-initdb.d/10-fixture.sql
        read_only: true
        bind:
          create_host_path: false
    healthcheck:
      test: [CMD-SHELL, pg_isready -h 127.0.0.1 -U postgres]
      interval: 1s
      timeout: 3s
      retries: 60
  baobei-api:
    image: ${{FIX_API_IMAGE:?fixture api image}}
    pull_policy: never
    entrypoint: [sleep, "86400"]
    healthcheck: {{test: [CMD, "true"], interval: 1s, retries: 30}}
    depends_on:
      baobei-postgres: {{condition: service_healthy}}
  baobei-web:
    image: ${{FIX_WEB_IMAGE:?fixture web image}}
    pull_policy: never
    entrypoint: [sleep, "86400"]
    healthcheck: {{test: [CMD, "true"], interval: 1s, retries: 30}}
    depends_on:
      baobei-api: {{condition: service_healthy}}
volumes:
  pgdata: {{}}
"""


class patch_attr:
    def __init__(self, obj, name, value):
        self.obj, self.name, self.value = obj, name, value

    def __enter__(self):
        self.old = getattr(self.obj, self.name)
        setattr(self.obj, self.name, self.value)

    def __exit__(self, *exc):
        setattr(self.obj, self.name, self.old)


class FixtureDocker:
    """Same compose()/call() surface as direct.Docker, bound to an isolated CI project."""

    def __init__(self, docker, project):
        self.docker, self.project = docker, project

    def call(self, *args, **kwargs):
        return self.docker.call(*args, **kwargs)

    def compose(self, release, secrets_dir, images, *args, capture=True, realm="crane"):
        env = {"COMPOSE_DISABLE_ENV_FILE": "1",
               "FIX_API_IMAGE": images["baobei-api"]["ref"], "FIX_WEB_IMAGE": images["baobei-web"]["ref"]}
        return self.docker.call("compose", "--project-name", self.project, "-f", str(release / "compose.yaml"),
                                *args, env=env, cwd=release, capture=capture)


def postgres_not_recreated(docker, work, manifest, postgres_image):
    """T3: a new release path changes the PostgreSQL config hash; start() must not touch it."""
    if sys.platform != "linux":
        print("DIRECT_POSTGRES_NOT_RECREATED=NOT_RUN_NON_LINUX")
        return
    project = "baobei-direct-pg-" + secrets.token_hex(4)
    assert not project.startswith("u-dao-")
    fixture = FixtureDocker(docker, project)
    password = secrets.token_hex(16)
    images, releases = {}, {}
    for label in ("old", "new"):
        context = work / ("image-" + label)
        context.mkdir()
        (context / "Dockerfile").write_text(
            f'FROM {manifest["images"]["baobei-web"]["ref"]}\nLABEL org.example.fixture-release="{label}"\n')
        images[label] = {}
        for service in direct.SERVICES:
            ref = f"{project}/{service}:{label}"
            docker.call("build", "--pull=false", "--label", "fixture-service=" + service,
                        "--tag", ref, str(context), capture=False)
            images[label][service] = {"ref": ref, "id": docker.call(
                "image", "inspect", "--format", "{{.Id}}", ref).decode().strip()}
        release = work / ("release-" + label)
        (release / "db").mkdir(parents=True)
        (release / "db/initialize.sql").write_text("CREATE TABLE fixture_marker (note text);\n")
        (release / "compose.yaml").write_text(FIXTURE.format(project=project, postgres=postgres_image,
                                                             password=password))
        releases[label] = release
    assert images["old"]["baobei-api"]["id"] != images["new"]["baobei-api"]["id"]
    old, new = releases["old"], releases["new"]

    def psql(container, sql):
        return docker.call("exec", container, "psql", "-U", "postgres", "-tAc", sql).decode().strip()

    def volume(container):
        return docker.call("inspect", "--format",
                           '{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}}{{end}}{{end}}', container).decode()

    def app_ids():
        return {s: docker.call("inspect", "--format", "{{.Image}}", fixture.compose(
            new, None, images["new"], "ps", "-q", s).decode().strip()).decode().strip() for s in direct.SERVICES}

    try:
        fixture.compose(old, None, images["old"], "up", "-d", "--no-build", "--pull", "missing",
                        "--wait", "--wait-timeout", "180", capture=False)
        pg_id, started = direct.database_identity(fixture, old, None, images["old"])
        psql(pg_id, "INSERT INTO fixture_marker VALUES ('kept')")
        pg_volume = volume(pg_id)
        hashes = [fixture.compose(r, None, images[k], "config", "--hash", "baobei-postgres").decode().split()[-1]
                  for k, r in (("old", old), ("new", new))]
        assert hashes[0] != hashes[1], "fixture must reproduce the release-path config hash change"
        direct.start(fixture, new, None, {"images": images["new"]})
        assert direct.database_identity(fixture, new, None, images["new"]) == (pg_id, started)
        assert volume(pg_id) == pg_volume
        assert psql(pg_id, "SELECT note FROM fixture_marker") == "kept"
        assert app_ids() == {s: images["new"][s]["id"] for s in direct.SERVICES}
        print("DIRECT_POSTGRES_NOT_RECREATED=PASS APP_SWITCHED=PASS")
        # G5-RC0-R1: machine drain check against real containers. The fixture
        # PostgreSQL uses its own superuser; the tool's read-only query is routed
        # to it, the container checks are the real ones.
        def fixture_read(dk, release, secrets_, imgs, realm, sql):
            return psql(pg_id, sql)
        with patch_attr(direct, "database_read", fixture_read):
            try:
                direct.verify_runtime_drained(fixture, new, None, images["new"], "crane")
            except ValueError as error:
                assert "仍处于" in str(error), error
            else:
                raise AssertionError("drain check accepted a running API")
            fixture.compose(new, None, images["new"], "stop", "baobei-api", capture=False)
            drained = direct.verify_runtime_drained(fixture, new, None, images["new"], "crane")
            assert drained["api_running"] == 0 and drained["business_connections"] == 0, drained
            psql(pg_id, "CREATE ROLE baobei_runtime LOGIN")
            # Hold one baobei_runtime connection open (local socket, fixture only).
            docker.call("exec", "-d", pg_id, "psql", "-U", "baobei_runtime", "-d", "postgres", "-c", "SELECT pg_sleep(30)")
            for _ in range(20):
                if psql(pg_id, "SELECT count(*) FROM pg_stat_activity WHERE usename='baobei_runtime'") != "0":
                    break
                import time
                time.sleep(0.5)
            try:
                direct.verify_runtime_drained(fixture, new, None, images["new"], "crane")
            except ValueError as error:
                assert "业务连接" in str(error), error
            else:
                raise AssertionError("drain check accepted an open baobei_runtime connection")
            fixture.compose(new, None, images["new"], "start", "baobei-api", capture=False)
        print("DIRECT_DRAIN_GUARD_REAL_CONTAINERS=PASS")
        # Control: the pre-fix whole-project up would recreate PostgreSQL in this same fixture.
        fixture.compose(new, None, images["new"], "up", "-d", "--no-build", "--pull", "never",
                        "--wait", "--wait-timeout", "180", capture=False)
        recreated, _ = direct.database_identity(fixture, new, None, images["new"])
        assert recreated != pg_id, "control did not reproduce the PostgreSQL recreate"
        assert psql(recreated, "SELECT note FROM fixture_marker") == "kept"
        print("DIRECT_POSTGRES_CONTROL_WHOLE_PROJECT_RECREATES=PASS")
    finally:
        # Only the throwaway fixture project is removed; never a u-dao-* project.
        fixture.compose(old, None, images["old"], "down", "-v", "--remove-orphans", capture=False)
        for label in images:
            for image in images[label].values():
                docker.call("image", "rm", image["ref"])


if __name__ == "__main__":
    test()
