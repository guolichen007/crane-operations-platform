#!/usr/bin/env python3
"""Build and deploy Baobei from a reviewed product commit using local Docker images."""

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
from datetime import timedelta
from hashlib import sha256
import hmac
from io import BytesIO
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
from zipfile import ZipFile

ROOT = Path(__file__).resolve().parents[2]
SOURCE = "https://github.com/guolichen007/crane-operations-platform"
PRODUCT_REF = "refs/remotes/origin/product/baobei-v1"
SHA = re.compile(r"[0-9a-f]{40}")
IMAGE_ID = re.compile(r"sha256:[0-9a-f]{64}")
SERVICES = ("baobei-web", "baobei-api")
WEBROOT = Path("/srv/u-dao/platform/ingress/acme-webroot")
SNAPSHOT = (
    "compose.production.yaml", "compose.direct.yaml", "compose.realm-baobei.yaml", "nginx.production.conf",
    "nginx.proxy.production.conf", "db/initialize.sql", "db/staff-role.sql",
)
SECRET_NAMES = ("db_password", "runtime_database_url", "staff_database_url", "oidc_client_secret")
# G5-RC0: the identity realm is an explicit, approved part of every release. It
# is chosen once at `prepare` (--identity-realm), written into the immutable
# manifest and bound to the Compose config hash; later commands read it from the
# verified manifest only. No environment variable can change it.
#   crane  : shared realm (current production default, no account service)
#   baobei : dedicated realm; loads compose.realm-baobei.yaml and requires the
#            account-service client secret.
REALMS = {
    "crane": {"overlay": None, "issuer": "https://crane.youdaozd.com/realms/crane",
              "client_id": "baobei-web", "account_client_id": None, "secrets": ()},
    "baobei": {"overlay": "compose.realm-baobei.yaml", "issuer": "https://crane.youdaozd.com/realms/baobei",
               "client_id": "baobei-web", "account_client_id": "baobei-account-service",
               "secrets": ("account_client_secret",)},
}
DEFAULT_REALM = "crane"


def realm_profile(realm):
    if realm not in REALMS:
        raise ValueError("身份 Realm 必须是批准的 crane 或 baobei")
    return REALMS[realm]


def realm_secrets(realm):
    return SECRET_NAMES + realm_profile(realm)["secrets"]


# A release is (source commit, identity realm). The crane release keeps the plain
# SHA as its id (existing state stays valid); other realms append "+<realm>".
RELEASE = re.compile(r"([0-9a-f]{40})(?:\+(baobei))?")


def release_id(source, realm):
    realm_profile(realm)
    return source if realm == DEFAULT_REALM else f"{source}+{realm}"


def parse_release(rid):
    match = RELEASE.fullmatch(rid or "")
    if not match:
        raise ValueError("无效的发布标识")
    return match.group(1), match.group(2) or DEFAULT_REALM
# Upgrades and rollbacks must never reconcile PostgreSQL: a new release directory changes
# the bind-mount source paths and therefore the database service config hash.
APP_UP = ("up", "-d", "--no-build", "--pull", "never", "--no-deps",
          "--wait", "--wait-timeout", "180", "baobei-api", "baobei-web")
DB_UP = ("up", "-d", "--no-build", "--pull", "missing",
         "--wait", "--wait-timeout", "180", "baobei-postgres")
PUBLIC_GO_PROXIES = ("https://proxy.golang.org,direct", "https://goproxy.cn,direct")
BUILD_INPUTS = {
    "apps/project-intake-web": (
        r"package(-lock)?\.json|index\.html|tsconfig[\w.-]*\.json|vite\.config\.ts|"
        r"src/(?:[\w-][\w.-]*/)*[\w-][\w.-]*\.(vue|ts|css|svg|png|woff2)"
    ),
    "services/project-intake-api": (
        r"go\.mod|go\.sum|database-grants\.sql|"
        r"(?![\w-]+_test\.go$)[\w-]+\.go|migrations/\d+_[\w-]+\.sql"
    ),
}


def run(args, *, cwd=ROOT, env=None, capture=True):
    result = subprocess.run(args, cwd=cwd, env=env, check=False,
                            stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.PIPE if capture else None)
    if result.returncode:
        raise ValueError(f"{args[0]} {args[1]} 失败（退出码 {result.returncode}）")
    return result.stdout if capture else None


def git(*args):
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    return run(["git", *args], env=env)


def verify_source(source):
    if not SHA.fullmatch(source):
        raise ValueError("SOURCE_SHA 必须为完整的 40 位小写 SHA")
    if git("rev-parse", "HEAD").decode().strip() != source:
        raise ValueError("HEAD 与 SOURCE_SHA 不一致")
    if git("status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none"):
        raise ValueError("工作区必须干净")
    origin = git("remote", "get-url", "origin").decode().strip()
    if origin not in (SOURCE, SOURCE + ".git",
                      "git@github.com:guolichen007/crane-operations-platform.git",
                      "ssh://git@github.com/guolichen007/crane-operations-platform.git"):
        raise ValueError("origin 必须为 Baobei 官方仓库")
    git("merge-base", "--is-ancestor", source, PRODUCT_REF)


# G5-RC1-R2: the release tools of the candidate commit are part of the release:
# copied at prepare, hashed into the manifest; preflight/switch only run with
# these exact bytes (the running direct-deploy.py must equal the snapshot, and
# admin-preflight.py is executed from the snapshot, never from the work tree).
TOOL_FILES = ("tools/direct-deploy.py", "tools/admin-preflight.py")


def export_source(source, dest):
    """Extract only regular tracked build inputs and runtime configuration."""
    selected = {"deploy/baobei/" + name for name in SNAPSHOT}
    selected.update("deploy/baobei/" + Path(name).name for name in TOOL_FILES)
    selected.add("deploy/baobei/nginx.conf")
    for name in ("api", "web"):
        selected.update(f"deploy/baobei/{name}.Dockerfile{ext}" for ext in ("", ".dockerignore"))
    paths = set(selected)
    for prefix, pattern in BUILD_INPUTS.items():
        listed = git("ls-tree", "-r", "-z", "--name-only", source, "--", prefix)
        for raw in listed.decode().split("\0"):
            if raw.startswith(prefix + "/") and re.fullmatch(pattern, raw[len(prefix) + 1:]):
                paths.add(raw)
    blob = git("archive", "--format=zip", source, "--", *sorted(paths))
    with ZipFile(BytesIO(blob)) as archive:
        for entry in archive.infolist():
            if entry.is_dir():
                continue
            mode = entry.external_attr >> 16
            if entry.filename not in paths or stat.S_IFMT(mode) not in (0, stat.S_IFREG):
                raise ValueError("构建输入包含非普通文件")
            dest_path = dest / entry.filename
            dest_path.parent.mkdir(parents=True, exist_ok=True)
            dest_path.write_bytes(archive.read(entry))


def require_file(path):
    if not path.is_file() or path.resolve() != path:
        raise ValueError(f"挂载源必须为已存在的普通文件 {path.name}")
    return path


def require_directory(path):
    if not path.is_dir() or path.resolve() != path:
        raise ValueError(f"必须预先创建真实目录 {path}")


def private_directory(path):
    require_directory(path)
    if os.name != "nt" and (path.stat().st_uid != os.geteuid() or path.stat().st_mode & 0o077):
        raise ValueError("状态目录必须由当前操作者拥有，权限 0700")


def canonical_json(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode()


def digest(value):
    return sha256(value).hexdigest()


def hash_files(root, names):
    return digest(canonical_json({name: digest(require_file(root / name).read_bytes()) for name in names}))


def database_contract(root):
    prefix = "services/project-intake-api/"
    migrations = sorted(path.relative_to(root).as_posix() for path in (root / prefix / "migrations").glob("*.sql"))
    if not migrations:
        raise ValueError("缺少数据库迁移契约")
    return hash_files(root, migrations + [prefix + "database-grants.sql",
                      "deploy/baobei/db/initialize.sql", "deploy/baobei/db/staff-role.sql"])


def atomic_json(path, value):
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as output:
        tmp = Path(output.name)
        try:
            output.write(canonical_json(value) + b"\n")
            output.flush()
            os.fsync(output.fileno())
        except BaseException:
            output.close()
            tmp.unlink()
            raise
    try:
        tmp.replace(path)
    finally:
        tmp.unlink(missing_ok=True)


def read_json(path):
    return json.loads(require_file(path).read_bytes())


@contextmanager
def deployment_lock(state):
    lock = state / ".lock"
    try:
        lock.mkdir(mode=0o700)
    except FileExistsError as err:
        raise ValueError("部署状态已锁定，请核对正在进行的操作") from err
    try:
        yield
    finally:
        lock.rmdir()


def local_ref(service, source):
    return f"baobei-local/{service}:{source}"


class Docker:
    def __init__(self):
        self.env = {k: v for k, v in os.environ.items()
                    if not k.startswith(("COMPOSE_", "DOCKER_", "BUILDX_", "BAOBEI_"))}
        context = os.environ.get("BAOBEI_DOCKER_CONTEXT") or self.call("context", "show").decode().strip()
        endpoints = json.loads(self.call("context", "inspect", context))
        host = endpoints[0]["Endpoints"]["docker"]["Host"]
        if not host.startswith(("unix://", "npipe://")):
            raise ValueError("必须使用本机 Docker Engine")
        self.context = context
        self.platform = self.call("version", "--format", "{{.Server.Os}}/{{.Server.Arch}}").decode().strip()
        if self.platform not in ("linux/amd64", "linux/arm64"):
            raise ValueError("需要 linux/amd64 或 linux/arm64 Docker Engine")

    def call(self, *args, capture=True, env=None, cwd=ROOT):
        context = ["--context", self.context] if hasattr(self, "context") else []
        return run(["docker", *context, *args], cwd=cwd, env={**self.env, **(env or {})}, capture=capture)

    def compose(self, release, secrets, images, *args, capture=True, realm=DEFAULT_REALM):
        overlay = realm_profile(realm)["overlay"]
        files = ["-f", "compose.production.yaml", "-f", "compose.direct.yaml"] + (["-f", overlay] if overlay else [])
        env = {
            "COMPOSE_DISABLE_ENV_FILE": "1",
            "BAOBEI_SECRETS_DIR": str(secrets),
            "BAOBEI_WEB_IMAGE": images["baobei-web"]["ref"],
            "BAOBEI_API_IMAGE": images["baobei-api"]["ref"],
        }
        return self.call("compose", "--project-name", "u-dao-baobei", *files,
                         *args, env=env, cwd=release, capture=capture)

    def inspect(self, ref, source):
        image, = json.loads(self.call("image", "inspect", ref))
        labels = image["Config"]["Labels"] or {}
        if (not IMAGE_ID.fullmatch(image["Id"]) or ref not in (image.get("RepoTags") or []) or
                labels.get("org.opencontainers.image.revision") != source or
                labels.get("org.opencontainers.image.source") != SOURCE or
                f'{image["Os"]}/{image["Architecture"]}' != self.platform):
            raise ValueError("本地镜像 ID、固定标签、OCI 来源或架构不匹配")
        return {"ref": ref, "id": image["Id"], "created_at": image["Created"]}


def check_identity(config, realm):
    """The rendered API service must carry exactly the approved realm wiring."""
    profile = realm_profile(realm)
    api = config["services"]["baobei-api"]
    env = api.get("environment") or {}
    secrets = {s["source"] if isinstance(s, dict) else s for s in api.get("secrets") or []}
    expected = {"BAOBEI_IDENTITY_REALM": realm, "OIDC_ISSUER": profile["issuer"], "OIDC_CLIENT_ID": profile["client_id"]}
    if any(env.get(k) != v for k, v in expected.items()):
        raise ValueError("渲染后的 API 身份配置与批准的 Realm 不一致")
    if profile["account_client_id"]:
        if (env.get("BAOBEI_ACCOUNT_CLIENT_ID") != profile["account_client_id"] or
                env.get("BAOBEI_ACCOUNT_CLIENT_SECRET_FILE") != "/run/secrets/account_client_secret" or
                "account_client_secret" not in secrets):
            raise ValueError("独立 Realm 缺少账号服务客户端或密钥挂载")
        source = (config.get("secrets") or {}).get("account_client_secret", {}).get("file", "")
        if not source.endswith("/account_client_secret"):
            raise ValueError("账号服务密钥必须来自外部密钥目录")
    elif "BAOBEI_ACCOUNT_CLIENT_ID" in env or "BAOBEI_ACCOUNT_CLIENT_SECRET_FILE" in env or "account_client_secret" in secrets:
        raise ValueError("共享 Realm 不得启用账号服务")


def config_hash(docker, release, secrets, images, realm=DEFAULT_REALM):
    config = json.loads(docker.compose(release, secrets, images, "config", "--no-path-resolution",
                                      "--format", "json", realm=realm))
    check_identity(config, realm)
    services = config["services"]
    if set(services) != {*SERVICES, "baobei-postgres"}:
        raise ValueError("直接部署服务集合有误")
    for service, spec in services.items():
        if spec.get("ports") or spec.get("build"):
            raise ValueError("部署配置禁止发布宿主端口或隐式构建")
        if service in SERVICES and (spec["image"] != images[service]["ref"] or spec["pull_policy"] != "never"):
            raise ValueError("直接部署必须使用固定的本地镜像")
    return digest(canonical_json(config))


def build_images(docker, source, staged, go_proxy):
    deploy = staged / "deploy/baobei"
    config = staged / "nginx-context"
    config.mkdir()
    shutil.copyfile(deploy / "nginx.conf", config / "nginx.conf")
    result = {}
    for service, kind, path in (
        ("baobei-web", "web", "apps/project-intake-web"),
        ("baobei-api", "api", "services/project-intake-api"),
    ):
        ref = local_ref(service, source)
        iid = staged / (kind + ".iid")
        extra = (["--build-context", "baobei_config=" + str(config)] if kind == "web"
                 else ["--build-arg", "GOPROXY=" + go_proxy])
        docker.call("build", "--platform", docker.platform, "--iidfile", str(iid),
                    "--tag", ref, "--build-arg", "SOURCE_SHA=" + source,
                    "--file", str(deploy / f"{kind}.Dockerfile"), *extra, str(staged / path),
                    capture=False)
        result[service] = docker.inspect(ref, source)
        if iid.read_text().strip() != result[service]["id"]:
            raise ValueError("构建返回的镜像 ID 与本地引擎不一致")
        command = ["nginx", "-v"] if kind == "web" else ["sh", "-ec", "test -x /usr/local/bin/baobei"]
        docker.call("run", "--rm", "--network", "none", "--pull", "never", "--entrypoint",
                    command[0], result[service]["id"], *command[1:])
    return result


def pointer(state, name):
    path = state / (name + ".json")
    if not path.exists():
        return None
    source = read_json(path)["source_sha"]
    if not RELEASE.fullmatch(source):
        raise ValueError("部署状态指针损坏")
    return source


def verify_release(docker, state, secrets, rid):
    source, realm = parse_release(rid)
    release = state / "releases" / rid
    require_directory(release)
    manifest = read_json(release / "manifest.json")
    # Releases prepared before G5-RC0 have no realm field: they are crane releases.
    if manifest.get("identity_realm", DEFAULT_REALM) != realm:
        raise ValueError("发布清单的身份 Realm 与发布标识不一致")
    if manifest.get("identity_issuer", REALMS[realm]["issuer"]) != REALMS[realm]["issuer"]:
        raise ValueError("发布清单的 Issuer 与批准的 Realm 不一致")
    if (manifest["mode"] != "direct-local" or manifest["source_sha"] != source or
            manifest["source"] != SOURCE or manifest["platform"] != docker.platform or
            manifest["snapshot_sha256"] != hash_files(release, snapshot_names(manifest))):
        raise ValueError("部署清单与当前 Docker 或 Compose 配置不一致")
    if set(manifest["images"]) != set(SERVICES):
        raise ValueError("清单必须包含 Web 和 API 镜像")
    for service, image in manifest["images"].items():
        if docker.inspect(local_ref(service, source), source) != image:
            raise ValueError("本地镜像已更改，请停止部署")
    if manifest["compose_sha256"] != config_hash(docker, release, secrets, manifest["images"], realm):
        raise ValueError("Compose 配置哈希不匹配")
    return release, manifest


def snapshot_names(manifest):
    """Pre-RC0 releases were snapshotted without the realm overlay."""
    return SNAPSHOT if "identity_realm" in manifest else tuple(n for n in SNAPSHOT if n != "compose.realm-baobei.yaml")


def prepare(docker, state, secrets, source, realm=DEFAULT_REALM):
    rid = release_id(source, realm)
    verify_source(source)
    go_proxy = os.environ.get("BAOBEI_GOPROXY", PUBLIC_GO_PROXIES[0])
    if go_proxy not in PUBLIC_GO_PROXIES:
        raise ValueError("BAOBEI_GOPROXY 必须使用规定的无凭据公共地址")
    releases = state / "releases"
    releases.mkdir(exist_ok=True)
    require_directory(releases)
    release = releases / rid
    if release.exists():
        verify_release(docker, state, secrets, rid)
        return release
    # The same commit prepared for the other realm: reuse its verified images
    # (never rebuild a tag that another release already pins).
    sibling = None
    for other in REALMS:
        other_id = release_id(source, other)
        if other != realm and (releases / other_id).exists():
            sibling = verify_release(docker, state, secrets, other_id)[1]["images"]
    with tempfile.TemporaryDirectory(dir=state, prefix="build-") as tmp:
        staged = Path(tmp)
        export_source(source, staged)
        snapshot = staged / "snapshot"
        snapshot.mkdir()
        for name in SNAPSHOT:
            dest = snapshot / name
            dest.parent.mkdir(exist_ok=True)
            shutil.copyfile(staged / "deploy/baobei" / name, dest)
        for name in TOOL_FILES:
            dest = snapshot / name
            dest.parent.mkdir(exist_ok=True)
            shutil.copyfile(staged / "deploy/baobei" / Path(name).name, dest)
        db_contract = database_contract(staged)
        images = sibling or build_images(docker, source, staged, go_proxy)
        manifest = {
            "mode": "direct-local", "source_sha": source, "source": SOURCE, "platform": docker.platform,
            "built_at": datetime.now(timezone.utc).isoformat(), "images": images, "go_proxy": go_proxy,
            "identity_realm": realm, "identity_issuer": realm_profile(realm)["issuer"],
            "compose_sha256": config_hash(docker, snapshot, secrets, images, realm),
            "snapshot_sha256": hash_files(snapshot, SNAPSHOT), "database_contract_sha256": db_contract,
            "tools_sha256": hash_files(snapshot, TOOL_FILES),
            "rollback_target": pointer(state, "active"),
        }
        atomic_json(snapshot / "manifest.json", manifest)
        snapshot.rename(release)
    return release


def check_mounts(secrets, command, migrator_dsn, realm=DEFAULT_REALM):
    require_directory(secrets)
    names = ("db_password",) if command == "db" else realm_secrets(realm) if command in ("up", "rollback") else ()
    for name in names:
        require_file(secrets / name)
    if command in ("up", "rollback"):
        require_directory(WEBROOT)
    if command == "migrate":
        if migrator_dsn is None or migrator_dsn.parent != secrets:
            raise ValueError("需要在外部密钥目录提供 --migrator-dsn")
        require_file(migrator_dsn)


def database_identity(docker, release, secrets, images, realm=DEFAULT_REALM):
    """Return the running PostgreSQL container identity; refuse if absent or unhealthy."""
    container = docker.compose(release, secrets, images, "ps", "-q", "baobei-postgres", realm=realm).decode().strip()
    if not container or len(container.split()) != 1:
        raise ValueError("PostgreSQL 未运行，拒绝切换应用；首次安装须先执行 db 步骤")
    state = docker.call("inspect", "--format",
                        "{{.Id}} {{.State.StartedAt}} {{if .State.Health}}{{.State.Health.Status}}{{end}}",
                        container).decode().split()
    if len(state) != 3 or state[2] != "healthy":
        raise ValueError("PostgreSQL 不健康，拒绝切换应用")
    return state[0], state[1]


def database_read(docker, release, secrets, images, realm, sql):
    """One read-only query inside the running PostgreSQL container (local socket,
    bootstrap role). Used only for release checks; never writes."""
    container = docker.compose(release, secrets, images, "ps", "-q", "baobei-postgres", realm=realm).decode().strip()
    if not container or len(container.split()) != 1:
        raise ValueError("PostgreSQL 未运行，无法核验")
    out = docker.call("exec", container, "psql", "-X", "-qAt", "-U", "baobei_bootstrap", "-d", "baobei",
                      "-v", "ON_ERROR_STOP=1", "-c", "BEGIN READ ONLY; " + sql + "; COMMIT;")
    return out.decode().strip()


def database_issuer(docker, release, secrets, images, realm):
    """The issuer the database currently trusts (identity_epoch), or None."""
    value = database_read(docker, release, secrets, images, realm,
                          "SELECT coalesce((SELECT issuer FROM identity_epoch), '')")
    return value or None


def verify_runtime_drained(docker, release, secrets, images, realm):
    """Machine check before an incompatible migration (G5-RC0-R1): the old API is
    not running, no runtime or staff connection is left, PostgreSQL is healthy.
    Independent of any recorded evidence."""
    api = docker.compose(release, secrets, images, "ps", "-a", "-q", "baobei-api", realm=realm).decode().split()
    for container in api:
        status = docker.call("inspect", "--format", "{{.State.Status}}", container).decode().strip()
        if status not in ("exited", "created", "dead"):
            raise ValueError(f"旧 baobei-api 仍处于 {status} 状态，拒绝迁移；先停止 API")
    connections = database_read(docker, release, secrets, images, realm,
                                "SELECT count(*) FROM pg_stat_activity WHERE usename IN ('baobei_runtime','baobei_staff_api')")
    if connections != "0":
        raise ValueError(f"仍有 {connections} 个业务连接（baobei_runtime / baobei_staff_api），拒绝迁移")
    return {"api_containers": len(api), "api_running": 0, "business_connections": 0}


def start(docker, release, secrets, manifest, realm=DEFAULT_REALM):
    """Reconcile only Web/API. PostgreSQL is created solely by the explicit db command."""
    images = manifest["images"]
    database = database_identity(docker, release, secrets, images, realm)
    docker.compose(release, secrets, images, *APP_UP, capture=False, realm=realm)
    if database_identity(docker, release, secrets, images, realm) != database:
        raise ValueError("PostgreSQL 容器在应用切换期间被改变，停止并人工核查")
    for service, image in images.items():
        container = docker.compose(release, secrets, images, "ps", "-q", service, realm=realm).decode().strip()
        if not container or docker.call("inspect", "--format", "{{.Image}}", container).decode().strip() != image["id"]:
            raise ValueError("运行容器的镜像 ID 与部署清单不匹配")


# G5-RC0 upgrade-order gate. A release whose database contract differs from the
# active one (an incompatible migration) may only be migrated and started inside
# a recorded maintenance window, phase by phase; each phase needs the evidence
# of the previous one. The tool records phases and refuses out-of-order steps;
# it never stops services, takes backups or switches realms by itself.
PHASES = ("MAINTENANCE_ENTER", "FRESH_BACKUP_VERIFIED", "OLD_API_DRAINED", "SCHEMA_UPGRADE",
          "IDENTITY_EPOCH_INIT", "IDENTITY_PRECHECK", "IDENTITY_SWITCH", "NEW_API_START", "WEB_START", "SMOKE_TEST",
          "RELEASE_COMPLETE")
EVIDENCE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:@/+-]{2,199}")
# Phases recorded by the operator with a reference to their evidence (backup
# id + checksum, drain proof, precheck report, smoke result); the rest are
# recorded by this tool when its own step succeeds.
OPERATOR_PHASES = {"MAINTENANCE_ENTER", "FRESH_BACKUP_VERIFIED", "OLD_API_DRAINED", "IDENTITY_EPOCH_INIT", "IDENTITY_PRECHECK",
                   "IDENTITY_SWITCH", "SMOKE_TEST", "RELEASE_COMPLETE"}


def window_path(state):
    return state / "maintenance.json"


def read_window(state):
    path = window_path(state)
    return read_json(path) if path.exists() else None


def window_done(window, phase):
    return any(entry["phase"] == phase for entry in window["phases"])


def record_phase(state, rid, phase, evidence, measured=None):
    """Append one phase with its evidence; refuses skipping or repeating a phase."""
    if phase not in PHASES or not EVIDENCE.fullmatch(evidence or ""):
        raise ValueError("阶段名或证据编号无效")
    window = read_window(state)
    if phase == "MAINTENANCE_ENTER":
        if window and not window_done(window, "RELEASE_COMPLETE"):
            raise ValueError("已有未完成的维护窗口")
        window = {"release": rid, "phases": []}
    elif not window or window["release"] != rid:
        raise ValueError("没有本发布的维护窗口，先执行 MAINTENANCE_ENTER")
    done = [entry["phase"] for entry in window["phases"]]
    expected = PHASES[len(done)]
    if phase != expected:
        raise ValueError(f"阶段顺序错误：下一步必须是 {expected}")
    # "kind" separates what a person recorded (evidence reference only) from what
    # this tool measured itself (machine-checked values are stored with it).
    entry = {"phase": phase, "evidence": evidence, "at": datetime.now(timezone.utc).isoformat(),
             "kind": "machine-verified" if measured is not None else "evidence-recorded"}
    if measured is not None:
        entry["measured"] = measured
    window["phases"].append(entry)
    atomic_json(window_path(state), window)
    return window


# G5-RC1: the takeover-administrator precheck is produced by this tool, never
# read from an editable file. `preflight` runs admin-preflight.py (real Keycloak
# account checks + real target-realm login), checks that the result is for this
# release, realm, issuer and approved administrator, and writes a record sealed
# with a key that exists only in the private state directory. IDENTITY_PRECHECK
# is recorded only from that run; `switch` re-verifies the record (same release,
# realm, issuer, administrator, not expired, seal intact) before it invokes
# identity-switch, and records IDENTITY_SWITCH only after the database trusts
# the target issuer.
PREFLIGHT_VALIDITY = timedelta(minutes=60)
UUID_TEXT = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
TOOL_PHASES = {"IDENTITY_PRECHECK", "IDENTITY_SWITCH"}


def operator_phase_allowed(phase, realm):
    if phase not in OPERATOR_PHASES or (realm != DEFAULT_REALM and phase in TOOL_PHASES):
        raise ValueError("该阶段由部署工具在对应步骤成功后自动记录（专属 Realm 的预检与切换只能用 preflight / switch）")


def seal_key(state, create=False):
    """32 random bytes, private to this state directory; never printed or exported."""
    path = state / ".preflight-key"
    if not path.exists():
        if not create:
            raise ValueError("身份预检记录无效：本状态目录没有预检密钥")
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "wb") as out:
            out.write(os.urandom(32))
    if os.name != "nt" and path.stat().st_mode & 0o077:
        raise ValueError("身份预检密钥权限必须为 0600")
    return path.read_bytes()


def seal(key, record):
    body = {k: v for k, v in record.items() if k != "seal"}
    return hmac.new(key, canonical_json(body), sha256).hexdigest()


def verify_release_tools(state, rid):
    """The approved tool bytes of this release (G5-RC1-R2). Docker-free, so the
    precheck can run before the window's application steps; checks the manifest
    identity, the snapshotted tools against the manifest, and that the running
    direct-deploy.py is byte-identical to the snapshot."""
    source, realm = parse_release(rid)
    release = state / "releases" / rid
    manifest_path = release / "manifest.json"
    if not manifest_path.is_file():
        raise ValueError("没有本发布的清单：先 prepare 候选 SHA（发布工具快照缺失）")
    manifest = read_json(manifest_path)
    if (manifest.get("source_sha") != source or manifest.get("source") != SOURCE or
            manifest.get("identity_realm", DEFAULT_REALM) != realm or
            manifest.get("identity_issuer", REALMS[realm]["issuer"]) != REALMS[realm]["issuer"]):
        raise ValueError("发布清单与批准的 SOURCE_SHA、Realm 或 Issuer 不一致")
    if "tools_sha256" not in manifest:
        raise ValueError("该发布没有受核验的发布工具快照（G5-RC1-R2 之前准备），请重新 prepare")
    hashes = {}
    for name in TOOL_FILES:
        path = release / name
        if not path.is_file() or path.is_symlink():
            raise ValueError("发布工具缺失或不是普通文件：" + name)
        hashes[name] = digest(path.read_bytes())
    if digest(canonical_json(hashes)) != manifest["tools_sha256"]:
        raise ValueError("发布工具与清单不一致（被替换或修改）")
    if digest(Path(__file__).resolve().read_bytes()) != hashes["tools/direct-deploy.py"]:
        raise ValueError("正在运行的 direct-deploy.py 不是本发布快照中的发布工具")
    return {"source_sha": source, "release": rid, "tools_sha256": manifest["tools_sha256"], "tool_hashes": hashes,
            "manifest_sha256": digest(manifest_path.read_bytes())}


def execute_preflight_tool(helper, tool_args, realm):
    """Run the snapshotted admin-preflight.py login (files only, no secret on the command line)."""
    args = [sys.executable, "-I", "-B", str(helper), "login", "--realm", realm]
    for name in ("base", "admin", "link", "service_secret_file", "password_file", "web_secret_file", "redirect"):
        value = tool_args.get(name)
        if not value:
            raise ValueError("预检参数缺失：--" + name.replace("_", "-"))
        args += ["--" + name.replace("_", "-"), str(value)]
    result = subprocess.run(args, check=False, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    try:
        return json.loads(result.stdout)
    except ValueError:
        return {}


def preflight(state, rid, staff_id, change, tool_args, now=None):
    _, realm = parse_release(rid)
    if realm == DEFAULT_REALM:
        raise ValueError("共享 Realm 不需要接管管理员预检")
    if not UUID_TEXT.fullmatch(staff_id or "") or not EVIDENCE.fullmatch(change or ""):
        raise ValueError("预检需要批准的管理员 staff_id 与变更编号")
    require_phase(state, rid, "IDENTITY_PRECHECK")
    tools = verify_release_tools(state, rid)
    result = execute_preflight_tool(state / "releases" / rid / "tools/admin-preflight.py", tool_args, realm)
    if verify_release_tools(state, rid) != tools:
        raise ValueError("预检期间发布工具或清单发生变化，未记录 IDENTITY_PRECHECK")
    admin, login = result.get("admin") or {}, result.get("login") or {}
    problems = []
    if result.get("ADMIN_ACCOUNT_VERIFIED") != "PASS" or result.get("ADMIN_LOGIN_VERIFIED") != "PASS":
        problems.append("账号核查或真实登录未通过")
    if result.get("realm") != realm or result.get("issuer") != REALMS[realm]["issuer"]:
        problems.append("结果不是目标 Realm / Issuer")
    if admin.get("staffId") != staff_id:
        problems.append("结果不是批准的管理员")
    kc = admin.get("keycloakSubject")
    if not UUID_TEXT.fullmatch(kc or "") or login.get("sub") != kc or login.get("roles") != ["baobei_admin"]:
        problems.append("真实登录的 subject 或角色与账号服务核查不一致")
    if not admin.get("businessSubject"):
        problems.append("缺少业务身份映射")
    if problems:
        raise ValueError("身份预检未通过，未记录 IDENTITY_PRECHECK：" + "；".join(problems))
    now = now or datetime.now(timezone.utc)
    record = {"release": rid, "realm": realm, "issuer": REALMS[realm]["issuer"], "staff_id": staff_id,
              "keycloak_subject": kc, "business_subject": admin["businessSubject"], "change": change,
              "generated_at": now.isoformat(), "expires_at": (now + PREFLIGHT_VALIDITY).isoformat(),
              "ADMIN_ACCOUNT_VERIFIED": "PASS", "ADMIN_LOGIN_VERIFIED": "PASS",
              # tool and release identity (G5-RC1-R2), re-checked before the switch
              "source_sha": tools["source_sha"], "tools_sha256": tools["tools_sha256"],
              "tool_hashes": tools["tool_hashes"], "manifest_sha256": tools["manifest_sha256"]}
    record["seal"] = seal(seal_key(state, create=True), record)
    atomic_json(state / "preflight.json", record)
    os.chmod(state / "preflight.json", 0o600)
    measured = {k: v for k, v in record.items() if k != "seal"}
    measured["record_sha256"] = digest(canonical_json(record))
    return record_phase(state, rid, "IDENTITY_PRECHECK", "preflight:" + change, measured)


def verify_preflight_record(state, rid, staff_id, now=None):
    path = state / "preflight.json"
    if not path.exists():
        raise ValueError("没有本发布的身份预检记录：先执行 preflight")
    record = read_json(path)
    key = seal_key(state)
    if not hmac.compare_digest(str(record.get("seal", "")), seal(key, record)):
        raise ValueError("身份预检记录被修改或来自其他状态目录")
    _, realm = parse_release(rid)
    if (record.get("release") != rid or record.get("realm") != realm or record.get("issuer") != REALMS[realm]["issuer"] or
            record.get("staff_id") != staff_id or record.get("ADMIN_ACCOUNT_VERIFIED") != "PASS" or
            record.get("ADMIN_LOGIN_VERIFIED") != "PASS"):
        raise ValueError("身份预检记录不属于本发布、Realm、Issuer 或管理员")
    if (now or datetime.now(timezone.utc)) > datetime.fromisoformat(record["expires_at"]):
        raise ValueError("身份预检记录已过期，请重新执行 preflight")
    tools = verify_release_tools(state, rid)
    if any(record.get(k) != tools[k] for k in ("source_sha", "tools_sha256", "tool_hashes", "manifest_sha256")):
        raise ValueError("身份预检记录与当前发布或发布工具不一致（发布版本或工具已变化）")
    return record


def switch_command(state, rid, staff_id, operator, reason, approval):
    record = verify_preflight_record(state, rid, staff_id)
    # Change binding: the switch completes exactly the change the precheck was
    # recorded for. Whether that change is actually approved is confirmed by the
    # independent release owner; this tool only enforces that the two are the same.
    if approval != record.get("change"):
        raise ValueError("切换的变更编号与身份预检的变更编号不一致：变更 %s 的预检不能用于其他变更" % record.get("change"))
    window = read_window(state)
    if not window or not any(e["phase"] == "IDENTITY_PRECHECK" and e.get("measured", {}).get("record_sha256") ==
                             digest(canonical_json(record)) for e in window["phases"]):
        raise ValueError("身份预检记录与维护窗口中的 IDENTITY_PRECHECK 不一致")
    return ["maintenance", "identity-switch", "--realm", record["realm"], "--operator", operator, "--reason", reason,
            "--takeover-admin", staff_id, "--approval", approval]


def identity_switch(docker, state, secrets, rid, staff_id, operator, reason, approval, maintenance_dsn):
    """Run the API's identity-switch after the precheck record re-verifies."""
    require_phase(state, rid, "IDENTITY_SWITCH")
    command = switch_command(state, rid, staff_id, operator, reason, approval)
    release, manifest = verify_release(docker, state, secrets, rid)
    if maintenance_dsn is None or maintenance_dsn.parent != secrets:
        raise ValueError("需要在外部密钥目录提供 --maintenance-dsn")
    require_file(maintenance_dsn)
    realm = manifest["identity_realm"]
    docker.call("run", "--rm", "--pull", "never", "--read-only", "--network", "u-dao-baobei-internal",
                "--security-opt", "no-new-privileges", "--env", "APP_ENV=production",
                "--env", "DATABASE_URL_FILE=/run/secrets/db_url",
                "--mount", f"type=bind,src={maintenance_dsn},dst=/run/secrets/db_url,readonly",
                manifest["images"]["baobei-api"]["id"], *command, capture=False)
    trusted = database_issuer(docker, release, secrets, manifest["images"], realm)
    if trusted != REALMS[realm]["issuer"]:
        raise ValueError("identity-switch 后数据库身份纪元未指向目标 Realm")
    return record_phase(state, rid, "IDENTITY_SWITCH", "switch:" + approval,
                        {"issuer": trusted, "takeover_admin": staff_id})


def require_phase(state, rid, phase):
    window = read_window(state)
    if not window or window["release"] != rid:
        raise ValueError(f"不兼容升级必须在维护窗口内进行：缺少 {phase}")
    done = [entry["phase"] for entry in window["phases"]]
    if PHASES.index(phase) != len(done):
        raise ValueError(f"阶段顺序错误：当前可执行 {PHASES[len(done)] if len(done) < len(PHASES) else '无'}，不能执行 {phase}")


def incompatible(docker, state, secrets, manifest, active):
    """True when the candidate changes the database contract of the active release."""
    if not active:
        return False
    return verify_release(docker, state, secrets, active)[1]["database_contract_sha256"] != manifest["database_contract_sha256"]


def apply(docker, state, secrets, rid, command, migrator_dsn, confirm_realm=None):
    if not sys.platform.startswith("linux"):
        raise ValueError("启动、迁移和回滚仅支持 Linux 部署宿主")
    source, realm = parse_release(rid)
    check_mounts(secrets, command, migrator_dsn, realm)
    release, manifest = verify_release(docker, state, secrets, rid)
    active, pending = pointer(state, "active"), pointer(state, "pending")
    if pending not in (None, rid):
        raise ValueError("另一提交有未完成部署，先恢复该提交")
    images = manifest["images"]
    if command == "rollback":
        target = manifest["rollback_target"]
        if not target or rid not in (active, pending):
            raise ValueError("没有本次部署的可用回滚目标")
        release, previous = verify_release(docker, state, secrets, target)
        if manifest["database_contract_sha256"] != previous["database_contract_sha256"]:
            raise ValueError("数据库契约不同，需要经审查的数据库恢复流程")
        target_realm = parse_release(target)[1]
        if target_realm != realm and confirm_realm != target_realm:
            raise ValueError("回滚目标使用另一身份 Realm：需先完成 identity-switch 并以 --confirm-realm 明确确认")
        check_mounts(secrets, command, migrator_dsn, target_realm)
        if target_realm != realm and database_issuer(docker, release, secrets, previous["images"], target_realm) != REALMS[target_realm]["issuer"]:
            raise ValueError("数据库身份纪元未指向回滚目标的 Realm：先执行 identity-switch")
        atomic_json(state / "pending.json", {"source_sha": rid})
        start(docker, release, secrets, previous, target_realm)
        atomic_json(state / "active.json", {"source_sha": target})
    else:
        active_source, active_realm = parse_release(active) if active else (None, DEFAULT_REALM)
        # Path A (G5-RC0-R1): the same commit prepared for the other approved realm.
        # Both releases are verified; same images and database contract; only the
        # identity realm differs. The original rollback baseline is kept untouched.
        same_commit_switch = (command == "up" and active not in (None, rid) and active_source == source
                              and active_realm != realm)
        if same_commit_switch:
            _, current = verify_release(docker, state, secrets, active)
            if (current["database_contract_sha256"] != manifest["database_contract_sha256"] or
                    current["images"] != manifest["images"]):
                raise ValueError("同提交 Realm 切换要求相同镜像与数据库契约")
        elif active not in (rid, manifest["rollback_target"]):
            raise ValueError("清单的回滚基线已过期")
        if command == "up" and active and active_realm != realm and confirm_realm != realm:
            raise ValueError("本发布改变身份 Realm：需先完成 identity-switch 并以 --confirm-realm 明确确认")
        gated = not same_commit_switch and incompatible(docker, state, secrets, manifest, active)
        if command == "migrate" and gated:
            require_phase(state, rid, "SCHEMA_UPGRADE")
        if command == "up" and gated:
            require_phase(state, rid, "NEW_API_START")
        atomic_json(state / "pending.json", {"source_sha": rid})
        if command == "db":
            docker.compose(release, secrets, images, *DB_UP, capture=False, realm=realm)
            return
        if command == "migrate":
            runtime = None
            if gated:
                # Evidence alone is not enough: measure the runtime state now.
                database = database_identity(docker, release, secrets, images, realm)
                runtime = verify_runtime_drained(docker, release, secrets, images, realm)
            docker.call("run", "--rm", "--pull", "never", "--read-only", "--network", "u-dao-baobei-internal",
                        "--security-opt", "no-new-privileges", "--env", "APP_ENV=production",
                        "--env", "DATABASE_URL_FILE=/run/secrets/db_url",
                        "--mount", f"type=bind,src={migrator_dsn},dst=/run/secrets/db_url,readonly",
                        images["baobei-api"]["id"], "maintenance", "migrate", capture=False)
            if gated:
                if database_identity(docker, release, secrets, images, realm) != database:
                    raise ValueError("PostgreSQL 容器在迁移期间被改变，停止并人工核查")
                record_phase(state, rid, "SCHEMA_UPGRADE", "migrate:" + manifest["database_contract_sha256"][:16],
                             measured={"runtime_drained": runtime, "postgres": database[0][:12]})
            return
        if "identity_realm" in manifest:
            # Every RC0+ release (schema ≥ 00006) starts only when the database
            # already trusts its issuer: an empty epoch (fresh 00005 → 00008
            # upgrade without IDENTITY_EPOCH_INIT) or another realm is refused
            # before Compose touches anything.
            trusted = database_issuer(docker, release, secrets, images, realm)
            if trusted != REALMS[realm]["issuer"]:
                raise ValueError("数据库身份纪元为空或未指向目标 Realm：先在维护窗口执行 identity-switch（首次须先建立共享 crane 纪元）")
        start(docker, release, secrets, manifest, realm)
        if gated:
            record_phase(state, rid, "NEW_API_START", "image:" + images["baobei-api"]["id"][7:23],
                         measured={"running_image": images["baobei-api"]["id"]})
            record_phase(state, rid, "WEB_START", "image:" + images["baobei-web"]["id"][7:23],
                         measured={"running_image": images["baobei-web"]["id"]})
        atomic_json(state / "active.json", {"source_sha": rid})
    (state / "pending.json").unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("prepare", "db", "migrate", "up", "rollback", "phase", "status",
                                            "preflight", "switch"))
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--identity-realm", choices=tuple(REALMS), default=DEFAULT_REALM,
                        help="批准的身份 Realm；默认 crane（现状）。写入不可变发布清单")
    parser.add_argument("--confirm-realm", choices=tuple(REALMS),
                        help="切换身份 Realm 的发布或回滚时，明确确认目标 Realm（须已完成 identity-switch）")
    parser.add_argument("--state-dir", type=Path, required=True)
    parser.add_argument("--secrets-dir", type=Path, required=True)
    parser.add_argument("--migrator-dsn", type=Path)
    parser.add_argument("--phase", choices=PHASES)
    parser.add_argument("--evidence")
    # preflight / switch (dedicated realm only)
    parser.add_argument("--takeover-admin", help="批准的接管管理员 staff_id")
    parser.add_argument("--change", help="变更编号（预检记录绑定）")
    parser.add_argument("--keycloak-base")
    parser.add_argument("--admin-row", type=Path, help="接管管理员业务行（私有 0600 文件）")
    parser.add_argument("--link-row", type=Path, help="身份链接行（私有 0600 文件）")
    parser.add_argument("--service-secret-file", type=Path)
    parser.add_argument("--web-secret-file", type=Path)
    parser.add_argument("--password-file", type=Path, help="接管管理员本人提供的私有密码文件")
    parser.add_argument("--redirect")
    parser.add_argument("--operator")
    parser.add_argument("--reason")
    parser.add_argument("--approval")
    parser.add_argument("--maintenance-dsn", type=Path)
    args = parser.parse_args()
    state, secrets = args.state_dir.absolute(), args.secrets_dir.absolute()
    if (state.is_relative_to(ROOT) or secrets.is_relative_to(ROOT) or secrets.is_relative_to(state) or
            state.is_relative_to(secrets)):
        raise ValueError("状态与密钥目录必须彼此独立并位于源码工作树之外")
    private_directory(state)
    rid = release_id(args.source_sha, args.identity_realm)
    with deployment_lock(state):
        if args.command == "phase":
            operator_phase_allowed(args.phase, args.identity_realm)
            window = record_phase(state, rid, args.phase, args.evidence)
            print("BAOBEI_PHASE_" + args.phase + "=RECORDED " + str(len(window["phases"])) + "/" + str(len(PHASES)))
            return
        if args.command == "status":
            print(json.dumps({"active": pointer(state, "active"), "pending": pointer(state, "pending"),
                              "maintenance": read_window(state)}, ensure_ascii=False, sort_keys=True))
            return
        if args.command == "preflight":
            window = preflight(state, rid, args.takeover_admin, args.change, {
                "base": args.keycloak_base, "admin": args.admin_row, "link": args.link_row,
                "service_secret_file": args.service_secret_file, "password_file": args.password_file,
                "web_secret_file": args.web_secret_file, "redirect": args.redirect})
            m = window["phases"][-1]["measured"]
            print("BAOBEI_PREFLIGHT=PASS realm=%s admin=%s expires_at=%s" % (m["realm"], m["staff_id"], m["expires_at"]))
            return
        docker = Docker()
        if args.command == "switch":
            identity_switch(docker, state, secrets, rid, args.takeover_admin, args.operator, args.reason, args.approval,
                            args.maintenance_dsn.absolute() if args.maintenance_dsn else None)
            print("BAOBEI_IDENTITY_SWITCH=PASS")
            return
        if args.command == "prepare":
            path = prepare(docker, state, secrets, args.source_sha, args.identity_realm)
            print(path / "manifest.json")
        else:
            apply(docker, state, secrets, rid, args.command,
                  args.migrator_dsn.absolute() if args.migrator_dsn else None, args.confirm_realm)
            print("BAOBEI_DIRECT_" + args.command.upper() + "=PASS")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, TypeError) as error:
        sys.exit(str(error))
