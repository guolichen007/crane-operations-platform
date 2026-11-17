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
# CR-08C: each realm has its own Web client secret file. crane keeps the
# existing name (oidc_client_secret, also used by the old releases and path B);
# the dedicated realm reads only baobei_oidc_client_secret (fail-closed when it
# is missing, never falling back to the crane file). The API always sees the
# same mount path (/run/secrets/oidc_client_secret); only the source differs.
REALMS = {
    "crane": {"overlay": None, "issuer": "https://crane.youdaozd.com/realms/crane",
              "client_id": "baobei-web", "account_client_id": None, "secrets": (),
              "web_secret": "oidc_client_secret"},
    "baobei": {"overlay": "compose.realm-baobei.yaml", "issuer": "https://crane.youdaozd.com/realms/baobei",
               "client_id": "baobei-web", "account_client_id": "baobei-account-service",
               "secrets": ("account_client_secret",), "web_secret": "baobei_oidc_client_secret"},
}
DEFAULT_REALM = "crane"
BASE_SECRETS = ("db_password", "runtime_database_url", "staff_database_url")


def realm_profile(realm):
    if realm not in REALMS:
        raise ValueError("身份 Realm 必须是批准的 crane 或 baobei")
    return REALMS[realm]


def realm_secrets(realm):
    profile = realm_profile(realm)
    return BASE_SECRETS + (profile["web_secret"],) + profile["secrets"]


def check_secret_isolation(secrets, realm):
    """CR-08C: the dedicated realm's Web secret must be its own file, not a copy
    of the shared crane secret (copying/overwriting one file is what isolation
    forbids). Compares contents in memory only; nothing is printed."""
    if realm == DEFAULT_REALM:
        return
    own = secrets / realm_profile(realm)["web_secret"]
    shared = secrets / REALMS[DEFAULT_REALM]["web_secret"]
    if not own.is_file():
        raise ValueError("专属 Realm 缺少自己的 Web 客户端密钥文件 " + own.name + "（不会回退到共享 Realm 的密钥）")
    if shared.is_file() and hmac.compare_digest(own.read_bytes().strip(), shared.read_bytes().strip()):
        raise ValueError("专属 Realm 的 Web 客户端密钥与共享 crane 密钥相同：拒绝（各 Realm 必须使用各自的客户端密钥）")


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


# G5-C-P0 (P0-LEGACY-01): releases prepared by the pre-RC0 tool have no realm
# fields and a compose.production.yaml without BAOBEI_IDENTITY_REALM. They are
# accepted only when they are one of these approved, already-deployed commits
# (strict allow list, never "the manifest lacks identity_realm"), and only with
# the legacy identity contract: shared crane issuer and client, no account
# service. Every other check (snapshot hash, images, Compose hash, services,
# database contract) is the same as for current releases.
LEGACY_RELEASES = frozenset({
    "253b4a8f8e612c2380355b83e8a790e8c9cd7d8a",  # G4-S4 review baseline
    "7b9558084d73f5210cdd08e8240883ecc1659de8",  # deployed in production (schema 00005)
})
LEGACY_SNAPSHOT = tuple(n for n in SNAPSHOT if n != "compose.realm-baobei.yaml")
LEGACY_MANIFEST_KEYS = frozenset({"mode", "source_sha", "source", "platform", "built_at", "images", "go_proxy",
                                  "compose_sha256", "snapshot_sha256", "database_contract_sha256", "rollback_target"})


def legacy_release(manifest, source):
    """True only for an approved pre-RC0 release in the exact old manifest format."""
    if "identity_realm" in manifest or "identity_issuer" in manifest or "tools_sha256" in manifest:
        return False
    if source not in LEGACY_RELEASES or set(manifest) != LEGACY_MANIFEST_KEYS:
        raise ValueError("发布清单缺少身份 Realm：只有批准的旧版发布可按旧格式校验")
    return True
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


def run(args, *, cwd=ROOT, env=None, capture=True, stdin=None):
    result = subprocess.run(args, cwd=cwd, env=env, check=False, stdin=stdin,
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

    def call(self, *args, capture=True, env=None, cwd=ROOT, stdin=None):
        context = ["--context", self.context] if hasattr(self, "context") else []
        return run(["docker", *context, *args], cwd=cwd, env={**self.env, **(env or {})}, capture=capture, stdin=stdin)

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


def release_web_secret(manifest, realm):
    """The Web secret file a release was prepared with. Releases prepared before
    CR-08 (no web_secret field) mounted oidc_client_secret for either realm and
    stay verifiable as they were; every new release records its realm's file."""
    return manifest.get("web_secret", REALMS[DEFAULT_REALM]["web_secret"]) if manifest else realm_profile(realm)["web_secret"]


def check_identity(config, realm, legacy=False, web_secret=None):
    """The rendered API service must carry exactly the approved realm wiring.

    legacy=True (approved pre-RC0 release only): the old Compose had no
    BAOBEI_IDENTITY_REALM; it must still name the crane issuer and client and
    must not carry the account service."""
    profile = realm_profile(realm)
    api = config["services"]["baobei-api"]
    env = api.get("environment") or {}
    secrets = {s["source"] if isinstance(s, dict) else s for s in api.get("secrets") or []}
    expected = {"BAOBEI_IDENTITY_REALM": realm, "OIDC_ISSUER": profile["issuer"], "OIDC_CLIENT_ID": profile["client_id"]}
    if legacy:
        if realm != DEFAULT_REALM or "BAOBEI_IDENTITY_REALM" in env:
            raise ValueError("旧版发布只能是不含 Realm 字段的共享 crane 配置")
        expected.pop("BAOBEI_IDENTITY_REALM")
    if any(env.get(k) != v for k, v in expected.items()):
        raise ValueError("渲染后的 API 身份配置与批准的 Realm 不一致")
    # CR-08C: the Web client secret mount comes from the realm's own file.
    web_source = (config.get("secrets") or {}).get("oidc_client_secret", {}).get("file", "")
    if (env.get("OIDC_CLIENT_SECRET_FILE") != "/run/secrets/oidc_client_secret" or "oidc_client_secret" not in secrets or
            Path(web_source).name != (web_secret or profile["web_secret"]) or "OIDC_CLIENT_SECRET" in env):
        raise ValueError("Web 客户端密钥必须来自本 Realm 专属的外部密钥文件 " + (web_secret or profile["web_secret"]))
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


def config_hash(docker, release, secrets, images, realm=DEFAULT_REALM, legacy=False, web_secret=None):
    config = json.loads(docker.compose(release, secrets, images, "config", "--no-path-resolution",
                                      "--format", "json", realm=realm))
    check_identity(config, realm, legacy, web_secret)
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
    # Approved pre-RC0 releases only (strict allow list + exact old manifest keys).
    legacy = legacy_release(manifest, source)
    if legacy and realm != DEFAULT_REALM:
        raise ValueError("旧版发布只能作为共享 crane 发布校验")
    if not legacy and (manifest.get("identity_realm") != realm or manifest.get("identity_issuer") != REALMS[realm]["issuer"]):
        raise ValueError("发布清单的身份 Realm 或 Issuer 与发布标识不一致")
    if (manifest["mode"] != "direct-local" or manifest["source_sha"] != source or
            manifest["source"] != SOURCE or manifest["platform"] != docker.platform or
            manifest["snapshot_sha256"] != hash_files(release, snapshot_names(manifest))):
        raise ValueError("部署清单与当前 Docker 或 Compose 配置不一致")
    if set(manifest["images"]) != set(SERVICES):
        raise ValueError("清单必须包含 Web 和 API 镜像")
    for service, image in manifest["images"].items():
        if docker.inspect(local_ref(service, source), source) != image:
            raise ValueError("本地镜像已更改，请停止部署")
    if manifest["compose_sha256"] != config_hash(docker, release, secrets, manifest["images"], realm, legacy,
                                                 release_web_secret(manifest, realm)):
        raise ValueError("Compose 配置哈希不匹配")
    return release, manifest


def snapshot_names(manifest):
    """Approved pre-RC0 releases were snapshotted without the realm overlay."""
    return LEGACY_SNAPSHOT if legacy_release(manifest, manifest.get("source_sha")) else SNAPSHOT


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
            "web_secret": realm_profile(realm)["web_secret"],   # CR-08C: name only, never the value
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
        check_secret_isolation(secrets, realm)
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
# CR-08A: a maintenance window has a kind; each kind has its own phase list, so
# no phase is ever recorded that did not happen (a same-commit realm switch has
# no SCHEMA_UPGRADE; a disaster recovery has no identity switch).
#   upgrade           : database upgrade (the G5-RC0 list above; also every
#                       window written before CR-08, which has no "kind")
#   realm-switch      : same commit, other approved realm (path A, both ways)
#   disaster-recovery : path B, restore the pre-upgrade backup and start the
#                       rollback-target release on it
REALM_SWITCH_PHASES = ("MAINTENANCE_ENTER", "SWITCH_BASELINE_VERIFIED", "IDENTITY_PRECHECK", "IDENTITY_SWITCH",
                       "NEW_API_START", "WEB_START", "SMOKE_TEST", "RELEASE_COMPLETE")
RECOVERY_PHASES = ("MAINTENANCE_ENTER", "RECOVERY_BACKUP_VERIFIED", "APP_STOPPED", "DATABASE_RESTORED",
                   "RECOVERY_VERIFIED", "OLD_API_START", "WEB_START", "SMOKE_TEST", "RELEASE_COMPLETE")
WINDOW_KINDS = {"upgrade": PHASES, "realm-switch": REALM_SWITCH_PHASES, "disaster-recovery": RECOVERY_PHASES}
ALL_PHASES = tuple(dict.fromkeys(PHASES + REALM_SWITCH_PHASES + RECOVERY_PHASES))
EVIDENCE = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:@/+-]{2,199}")
# Phases recorded by the operator with a reference to their evidence (backup
# id + checksum, drain proof, precheck report, smoke result); the rest are
# recorded by this tool when its own step succeeds.
OPERATOR_PHASES = {"MAINTENANCE_ENTER", "FRESH_BACKUP_VERIFIED", "OLD_API_DRAINED", "IDENTITY_EPOCH_INIT", "IDENTITY_PRECHECK",
                   "IDENTITY_SWITCH", "SMOKE_TEST", "RELEASE_COMPLETE"}
OPERATOR_PHASES_BY_KIND = {
    "upgrade": OPERATOR_PHASES,
    "realm-switch": {"MAINTENANCE_ENTER", "IDENTITY_PRECHECK", "IDENTITY_SWITCH", "SMOKE_TEST", "RELEASE_COMPLETE"},
    "disaster-recovery": {"MAINTENANCE_ENTER", "SMOKE_TEST", "RELEASE_COMPLETE"},
}


def window_path(state):
    return state / "maintenance.json"


def read_window(state):
    path = window_path(state)
    return read_json(path) if path.exists() else None


def window_done(window, phase):
    return any(entry["phase"] == phase for entry in window["phases"])


def window_kind(window):
    """Windows written before CR-08 have no kind: they are upgrade windows."""
    return window.get("kind", "upgrade")


def window_phases(window):
    return WINDOW_KINDS[window_kind(window)]


def window_id(window):
    """Stable id; pre-CR-08 windows have none and use their opening entry."""
    return window.get("id") or digest(canonical_json(window["phases"][0]))[:16]


def window_closed(window):
    return window.get("status") in ("COMPLETE", "ABORTED", "SUPERSEDED") or window_done(window, "RELEASE_COMPLETE")


def next_phase(window):
    phases, done = window_phases(window), len(window["phases"])
    return None if window_closed(window) or done >= len(phases) else phases[done]


def archive_window(state, window):
    """Closed windows are kept (never deleted) under maintenance-history/."""
    history = state / "maintenance-history"
    history.mkdir(mode=0o700, exist_ok=True)
    target = history / (window_id(window) + ".json")
    if not target.exists():
        atomic_json(target, window)


def open_window(state, rid, evidence, kind):
    if kind not in WINDOW_KINDS:
        raise ValueError("维护窗口类型必须是 upgrade、realm-switch 或 disaster-recovery")
    window = read_window(state)
    if window and not window_closed(window):
        raise ValueError("已有未完成的维护窗口（%s，发布 %s，下一步 %s）：先完成，或用 abort-window 安全中止" %
                         (window_kind(window), window["release"], next_phase(window)))
    if window:
        archive_window(state, window)
    now = datetime.now(timezone.utc).isoformat()
    return {"id": os.urandom(8).hex(), "kind": kind, "release": rid, "status": "OPEN", "opened_at": now, "phases": []}


def record_phase(state, rid, phase, evidence, measured=None, kind="upgrade"):
    """Append one phase with its evidence; refuses skipping or repeating a phase."""
    if phase not in ALL_PHASES or not EVIDENCE.fullmatch(evidence or ""):
        raise ValueError("阶段名或证据编号无效")
    window = read_window(state)
    if phase == "MAINTENANCE_ENTER":
        window = open_window(state, rid, evidence, kind)
    elif not window or window["release"] != rid or window_closed(window):
        raise ValueError("没有本发布的维护窗口，先执行 MAINTENANCE_ENTER")
    expected = next_phase(window)
    if phase != expected:
        raise ValueError(f"阶段顺序错误：下一步必须是 {expected}")
    if (phase in ("SMOKE_TEST", "RELEASE_COMPLETE") and "id" in window and
            (pointer(state, "active") != rid or pointer(state, "pending") is not None)):
        raise ValueError(f"发布尚未生效（active 不是本发布或仍有 pending），不能记录 {phase}")
    # "kind" separates what a person recorded (evidence reference only) from what
    # this tool measured itself (machine-checked values are stored with it).
    entry = {"phase": phase, "evidence": evidence, "at": datetime.now(timezone.utc).isoformat(),
             "kind": "machine-verified" if measured is not None else "evidence-recorded"}
    if measured is not None:
        entry["measured"] = measured
    window["phases"].append(entry)
    if phase == "RELEASE_COMPLETE":
        window["status"] = "COMPLETE"
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


def operator_phase_allowed(phase, realm, kind="upgrade"):
    if (phase not in OPERATOR_PHASES_BY_KIND.get(kind, ()) or
            (realm != DEFAULT_REALM and phase in TOOL_PHASES)):
        raise ValueError("该阶段由部署工具在对应步骤成功后自动记录（专属 Realm 的预检与切换只能用 preflight / switch；"
                         "Realm 切换基线、灾难恢复步骤只能用 switch-baseline / recover）")


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


def preflight(state, rid, staff_id, change, tool_args, now=None, secrets=None):
    _, realm = parse_release(rid)
    if realm == DEFAULT_REALM:
        raise ValueError("共享 Realm 不需要接管管理员预检")
    if not UUID_TEXT.fullmatch(staff_id or "") or not EVIDENCE.fullmatch(change or ""):
        raise ValueError("预检需要批准的管理员 staff_id 与变更编号")
    window = require_phase(state, rid, "IDENTITY_PRECHECK")
    secret_mac = None
    if secrets is not None:
        # CR-08C: the real login uses exactly the realm's own Web secret file
        # from the release secret directory (the one the API will mount).
        own = secrets / realm_profile(realm)["web_secret"]
        given = tool_args.get("web_secret_file")
        if not given or Path(given).absolute() != own:
            raise ValueError("预检的 --web-secret-file 必须是密钥目录中的 " + own.name)
        check_secret_isolation(secrets, realm)
        secret_mac = web_secret_mac(state, secrets, realm)
    tools = verify_release_tools(state, rid)
    result = execute_preflight_tool(state / "releases" / rid / "tools/admin-preflight.py", tool_args, realm)
    if verify_release_tools(state, rid) != tools:
        raise ValueError("预检期间发布工具或清单发生变化，未记录 IDENTITY_PRECHECK")
    if secret_mac and web_secret_mac(state, secrets, realm) != secret_mac:
        raise ValueError("预检期间 Web 客户端密钥文件发生变化，未记录 IDENTITY_PRECHECK")
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
    if "id" in window:
        # CR-08A: bound to this maintenance window; a record from an earlier
        # window (even for the same release and change) never verifies again.
        record["window_id"] = window["id"]
    if secret_mac:
        record["web_secret_mac"] = secret_mac
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
    window = read_window(state)
    if "window_id" in record and (not window or window.get("id") != record["window_id"] or window_closed(window)):
        raise ValueError("身份预检记录属于另一个维护窗口：旧预检不能用于新的切换")
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


def start_phases(state, rid, kind, api_phase):
    """The start phases still to record. A retry after an interruption between
    the start and the pointer update resumes (records only what is missing)
    instead of being stuck behind an already-recorded phase."""
    window = read_window(state)
    if not window or window["release"] != rid or window_closed(window):
        raise ValueError(f"不兼容升级、Realm 切换与灾难恢复必须在本发布的维护窗口内进行：缺少 {api_phase}")
    if window_kind(window) != kind:
        raise ValueError(f"当前维护窗口类型为 {window_kind(window)}，本操作需要 {kind} 窗口")
    nxt = next_phase(window)
    if nxt == api_phase:
        return (api_phase, "WEB_START")
    if nxt == "WEB_START" and window_done(window, api_phase):
        return ("WEB_START",)
    if nxt == "SMOKE_TEST" and pointer(state, "pending") is not None and window_done(window, "WEB_START"):
        return ()
    raise ValueError(f"阶段顺序错误：当前可执行 {nxt or '无'}，不能执行 {api_phase}")


def require_phase(state, rid, phase, kind=None):
    window = read_window(state)
    if not window or window["release"] != rid or window_closed(window):
        raise ValueError(f"不兼容升级、Realm 切换与灾难恢复必须在本发布的维护窗口内进行：缺少 {phase}")
    if kind and window_kind(window) != kind:
        raise ValueError(f"当前维护窗口类型为 {window_kind(window)}，本操作需要 {kind} 窗口")
    expected = next_phase(window)
    if expected != phase:
        raise ValueError(f"阶段顺序错误：当前可执行 {expected or '无'}，不能执行 {phase}")
    return window


def abort_window(state, rid, evidence):
    """CR-08A: an interrupted window is closed explicitly as ABORTED (kept, with
    the step it stopped at). It never changes active/pending and never reports
    success; a pending release still has to be resumed, rolled back or recovered."""
    if not EVIDENCE.fullmatch(evidence or ""):
        raise ValueError("中止维护窗口需要证据编号")
    window = read_window(state)
    if not window or window["release"] != rid or window_closed(window):
        raise ValueError("没有本发布的未完成维护窗口")
    window.update({"id": window_id(window), "kind": window_kind(window), "status": "ABORTED",
                   "aborted": {"at": datetime.now(timezone.utc).isoformat(), "evidence": evidence,
                               "next_phase": next_phase(window), "active": pointer(state, "active"),
                               "pending": pointer(state, "pending")}})
    atomic_json(window_path(state), window)
    archive_window(state, window)
    return window


def status_report(state):
    """What is active, what is pending, and what the open window needs next."""
    active, pending, window = pointer(state, "active"), pointer(state, "pending"), read_window(state)
    report = {"active": active, "pending": pending, "maintenance": window}
    if window:
        report["window"] = {"kind": window_kind(window), "release": window["release"],
                            "status": window.get("status") or ("COMPLETE" if window_closed(window) else "OPEN"),
                            "next_phase": next_phase(window)}
    if pending:
        report["attention"] = "存在未完成部署 pending=%s：重试同一发布、执行兼容回滚或灾难恢复；不得视为已回退" % pending
    elif window and not window_closed(window):
        report["attention"] = "维护窗口未完成：继续 %s，或以 abort-window 中止（不改变发布指针）" % next_phase(window)
    return report


# ---------------------------------------------------------------------------
# CR-08: database facts read by the tool itself (inside the PostgreSQL
# container, bootstrap role over the local socket). Read-only except the
# reviewed disaster-recovery restore below.
APP_ROLES = ("baobei_migrator", "baobei_runtime", "baobei_maintenance", "baobei_staff_api")
DATABASE = "baobei"
SAFE_NAME = re.compile(r"[a-z_][a-z0-9_]{0,62}")
MIGRATION_FILE = re.compile(r"services/project-intake-api/migrations/\d+_[\w-]+\.sql")
CONTRACT_EXTRA = ("services/project-intake-api/database-grants.sql", "deploy/baobei/db/initialize.sql",
                  "deploy/baobei/db/staff-role.sql")


def source_blob(source, path):
    return git("show", f"{source}:{path}")


def source_migrations(source):
    listed = git("ls-tree", "-r", "-z", "--name-only", source, "--", "services/project-intake-api/migrations")
    return sorted(n for n in listed.decode().split("\0") if MIGRATION_FILE.fullmatch(n))


def expected_ledger(source, manifest):
    """The exact baobei_migrations rows the release's API requires, from the
    reviewed commit itself, bound to the manifest's database contract."""
    names = source_migrations(source)
    blobs = {name: source_blob(source, name) for name in (*names, *CONTRACT_EXTRA)}
    if not names or digest(canonical_json({n: digest(b) for n, b in blobs.items()})) != manifest["database_contract_sha256"]:
        raise ValueError("提交中的迁移文件与发布清单的数据库契约不一致")
    return [{"name": Path(n).name, "checksum": digest(blobs[n])} for n in names]


def database_query(docker, container, sql, database=DATABASE, read_only=True):
    if not SAFE_NAME.fullmatch(database):
        raise ValueError("数据库名无效")
    body = ("BEGIN READ ONLY; " + sql + "; COMMIT;") if read_only else sql
    return docker.call("exec", container, "psql", "-X", "-qAt", "-U", "baobei_bootstrap", "-d", database,
                       "-v", "ON_ERROR_STOP=1", "-c", body).decode().strip()


def postgres_container(docker, release, secrets, images, realm):
    container = docker.compose(release, secrets, images, "ps", "-q", "baobei-postgres", realm=realm).decode().strip()
    if not container or len(container.split()) != 1:
        raise ValueError("PostgreSQL 未运行，无法核验")
    return container


LEDGER_SQL = ("SELECT coalesce(json_agg(json_build_object('name',name,'checksum',checksum) ORDER BY name),'[]') "
              "FROM baobei_migrations")
# Two statements: PostgreSQL resolves table names at parse time, so the
# issuer is only queried after the table is known to exist (old schemas have none).
EPOCH_EXISTS_SQL = "SELECT to_regclass('public.identity_epoch') IS NOT NULL"
EPOCH_SQL = "SELECT coalesce((SELECT issuer FROM identity_epoch),'')"


def privileges_sql(database):
    roles = ",".join("'%s'" % r for r in APP_ROLES)
    return f"""SELECT json_build_object(
 'roles', (SELECT json_object_agg(rolname, json_build_object(
   'login', rolcanlogin,
   'elevated', rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls,
   'memberships', (SELECT count(*) FROM pg_auth_members m WHERE m.member = r.oid),
   'connect', has_database_privilege(rolname, '{database}', 'CONNECT'),
   'create', has_database_privilege(rolname, '{database}', 'CREATE'),
   'temp', has_database_privilege(rolname, '{database}', 'TEMP'),
   'other_databases', EXISTS (SELECT 1 FROM pg_database o WHERE o.datname NOT IN ('{database}', '{DATABASE}') AND o.datallowconn
     AND has_database_privilege(rolname, o.datname, 'CONNECT')),
   'schema_usage', has_schema_privilege(rolname, 'public', 'USAGE'),
   'schema_create', has_schema_privilege(rolname, 'public', 'CREATE'))) FROM pg_roles r WHERE rolname IN ({roles})),
 'public_database', (SELECT count(*) FROM pg_database d, aclexplode(coalesce(d.datacl, acldefault('d', d.datdba))) a
   WHERE d.datname = '{database}' AND a.grantee = 0),
 'public_schema', (SELECT count(*) FROM pg_namespace n, aclexplode(coalesce(n.nspacl, acldefault('n', n.nspowner))) a
   WHERE n.nspname = 'public' AND a.grantee = 0),
 'public_relations', (SELECT count(*) FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace,
   aclexplode(c.relacl) a WHERE s.nspname = 'public' AND a.grantee = 0),
 'foreign_owned', (SELECT count(*) FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'public' AND c.relkind IN ('r','p','v','m','S','f')
   AND pg_get_userbyid(c.relowner) <> 'baobei_migrator'
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')))"""


def check_privileges(observed):
    """CR-08B: the approved minimal database-level privilege set (initialize.sql
    + staff-role.sql); anything wider or missing is refused."""
    problems = []
    roles = observed.get("roles") or {}
    for role in APP_ROLES:
        r = roles.get(role)
        if not r:
            problems.append(role + " 缺失")
            continue
        expected = {"login": True, "elevated": False, "memberships": 0, "connect": True, "create": False, "temp": False,
                    "other_databases": False, "schema_usage": True, "schema_create": role == "baobei_migrator"}
        wrong = sorted(k for k, v in expected.items() if r.get(k) != v)
        if wrong:
            problems.append(role + " " + ",".join(wrong))
    for key in ("public_database", "public_schema", "public_relations", "foreign_owned"):
        if observed.get(key) != 0:
            problems.append(key)
    if problems:
        raise ValueError("数据库权限不是批准的最小集合：" + "；".join(problems))
    return True


def database_facts(docker, container, database=DATABASE):
    return {"ledger": json.loads(database_query(docker, container, LEDGER_SQL, database)),
            "epoch": (database_query(docker, container, EPOCH_SQL, database)
                      if database_query(docker, container, EPOCH_EXISTS_SQL, database) == "t" else "absent"),
            "privileges": json.loads(database_query(docker, container, privileges_sql(database), database))}


def check_database_for(facts, ledger, manifest, realm):
    """Ledger exactly the release's migrations; minimal privileges; identity
    state matching the release (old releases have no identity_epoch at all)."""
    if facts["ledger"] != ledger:
        raise ValueError("迁移 ledger 与目标发布不一致（%d 行，期望 %d 行）" % (len(facts["ledger"]), len(ledger)))
    check_privileges(facts["privileges"])
    if "identity_realm" in manifest:
        if facts["epoch"] != REALMS[realm]["issuer"]:
            raise ValueError("数据库身份纪元与目标发布的 Realm 不一致")
    elif facts["epoch"] != "absent":
        raise ValueError("旧版发布的数据库不应存在身份纪元（未恢复到旧 Schema）")
    return {"ledger_rows": len(ledger), "ledger_sha256": digest(canonical_json(ledger)), "identity": facts["epoch"],
            "privileges": "minimal"}


def running_identity(docker, release, secrets, images, realm, manifest):
    """The real API container: image, realm wiring and Web secret source."""
    container = docker.compose(release, secrets, images, "ps", "-q", "baobei-api", realm=realm).decode().strip()
    if not container or len(container.split()) != 1:
        raise ValueError("baobei-api 未运行")
    info = json.loads(docker.call("inspect", "--format",
                                  "[{{json .Image}},{{json .Config.Env}},{{json .Mounts}},{{json .State.Status}}]",
                                  container).decode().strip())
    image, env_list, mounts, status = info
    env = dict(item.split("=", 1) for item in env_list if "=" in item)
    profile = realm_profile(realm)
    expected = {"OIDC_ISSUER": profile["issuer"], "OIDC_CLIENT_ID": profile["client_id"],
                "OIDC_CLIENT_SECRET_FILE": "/run/secrets/oidc_client_secret"}
    if "identity_realm" in manifest:
        expected["BAOBEI_IDENTITY_REALM"] = realm
    secret = [m for m in mounts or [] if m.get("Destination") == "/run/secrets/oidc_client_secret"]
    web_secret = release_web_secret(manifest, realm)
    if (image != images["baobei-api"]["id"] or status != "running" or any(env.get(k) != v for k, v in expected.items()) or
            "OIDC_CLIENT_SECRET" in env or len(secret) != 1 or Path(secret[0].get("Source", "")).name != web_secret):
        raise ValueError("运行中的 API 容器与目标发布的镜像、Realm 或 Web 密钥来源不一致")
    return {"api_image": image, "realm": realm, "issuer": profile["issuer"], "web_secret": web_secret}


def auth_probe(docker, release, secrets, images, realm):
    """Authentication is configured end to end: through the running Web proxy the
    API reports a production OIDC login (discovery against the realm's issuer
    succeeded at start) and no development login."""
    web = docker.compose(release, secrets, images, "ps", "-q", "baobei-web", realm=realm).decode().strip()
    if not web or len(web.split()) != 1:
        raise ValueError("baobei-web 未运行")
    out = docker.call("exec", web, "wget", "-qO-", "http://127.0.0.1:8080/auth/config").decode().strip()
    try:
        config = json.loads(out)
    except ValueError:
        config = {}
    if config.get("oidc") is not True or config.get("development") is not False:
        raise ValueError("认证检查未通过：/auth/config 未报告生产 OIDC 登录")
    return {"auth_config": "oidc"}


def web_secret_mac(state, secrets, realm):
    """Keyed fingerprint (state-directory seal key) of the realm's Web secret:
    proves the precheck and the release use the same file content without ever
    storing or printing the value."""
    path = require_file(secrets / realm_profile(realm)["web_secret"])
    return hmac.new(seal_key(state, create=True), b"baobei-web-secret:" + path.read_bytes().strip(), sha256).hexdigest()


def check_precheck_secret(state, secrets, rid, realm):
    """The Web secret used now must be the one the takeover administrator's real
    login used in this window's precheck (dedicated realm, CR-08 windows)."""
    window = read_window(state)
    if realm == DEFAULT_REALM or not window or window["release"] != rid or "id" not in window:
        return
    macs = [e["measured"].get("web_secret_mac") for e in window["phases"]
            if e["phase"] == "IDENTITY_PRECHECK" and "measured" in e]
    if not macs or not macs[-1] or not hmac.compare_digest(macs[-1], web_secret_mac(state, secrets, realm)):
        raise ValueError("Web 客户端密钥与身份预检时使用的密钥不一致：重新执行 preflight")


def realm_switch_baseline(docker, state, secrets, rid):
    """CR-08A path A: the same commit, the other approved realm. Records
    SWITCH_BASELINE_VERIFIED from measurements only: both releases verify, same
    images and database contract, the live ledger is exactly the commit's
    migrations, privileges are minimal, the database still trusts the active
    realm, and the target realm's own secret files are present."""
    require_phase(state, rid, "SWITCH_BASELINE_VERIFIED", kind="realm-switch")
    source, realm = parse_release(rid)
    active, pending = pointer(state, "active"), pointer(state, "pending")
    if pending is not None:
        raise ValueError("存在未完成部署 pending，不能开始 Realm 切换")
    if not active or active == rid or parse_release(active)[0] != source:
        raise ValueError("Realm 切换只用于同一提交的另一批准 Realm：当前 active 不符")
    active_realm = parse_release(active)[1]
    check_mounts(secrets, "up", None, realm)
    release, manifest = verify_release(docker, state, secrets, rid)
    current_release, current = verify_release(docker, state, secrets, active)
    if current["images"] != manifest["images"] or current["database_contract_sha256"] != manifest["database_contract_sha256"]:
        raise ValueError("同提交 Realm 切换要求相同镜像与数据库契约")
    postgres = database_identity(docker, current_release, secrets, current["images"], active_realm)
    container = postgres_container(docker, current_release, secrets, current["images"], active_realm)
    measured = check_database_for(database_facts(docker, container), expected_ledger(source, manifest), current, active_realm)
    measured.update({"active": active, "target": rid, "images": {s: i["id"] for s, i in manifest["images"].items()},
                     "database_contract_sha256": manifest["database_contract_sha256"], "postgres": postgres[0][:12]})
    return record_phase(state, rid, "SWITCH_BASELINE_VERIFIED", "baseline:" + manifest["database_contract_sha256"][:16],
                        measured)


# CR-08B path B: restore the verified pre-upgrade backup and start the release
# the failed one was prepared from (its rollback_target). Every step measures
# its own preconditions; the database is restored into a fresh database and
# swapped in one transaction only after it verifies (the failed database is
# kept, renamed, with no application access). active/pending move only after
# the old release runs healthy on the restored, verified database.
BACKUP_FORMAT = "baobei-backup-v1"


def recovery_context(docker, state, secrets, rid, window):
    measured = window["phases"][1]["measured"] if len(window["phases"]) > 1 else None
    source, realm = parse_release(rid)
    target_release, target = verify_release(docker, state, secrets, rid)
    return source, realm, target_release, target, measured


def verify_backup(archive, ledger):
    archive = Path(archive)
    sidecar = Path(str(archive) + ".json")
    if not archive.is_absolute() or archive.resolve() != archive or not archive.is_file() or not sidecar.is_file():
        raise ValueError("备份必须是绝对路径的普通文件，并带有 .json 清单")
    if archive.is_relative_to(ROOT):
        raise ValueError("备份不得位于源码工作树内")
    meta = json.loads(sidecar.read_bytes())
    h = sha256()
    with archive.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    if meta.get("format") != BACKUP_FORMAT or meta.get("database") != DATABASE or meta.get("environment", "production") != "production":
        raise ValueError("备份清单格式、数据库或环境不正确")
    if not hmac.compare_digest(h.hexdigest(), str(meta.get("sha256", ""))):
        raise ValueError("备份校验和与清单不一致")
    if meta.get("migrations") != ledger:
        raise ValueError("备份的迁移 ledger 不是目标旧版本的 Schema（不是升级前备份）")
    counts = meta.get("counts")
    if not isinstance(counts, dict) or not counts or not all(SAFE_NAME.fullmatch(t) for t in counts):
        raise ValueError("备份清单缺少表计数")
    return meta, h.hexdigest()


def upgrade_releases(docker, state, secrets, failed, failed_manifest, rid):
    """The releases whose upgrade window counts as the failed release's upgrade.

    The failed release itself, plus the same commit prepared for the other
    approved realm (path A may have switched realms after the upgrade window,
    which belongs to the release id it was opened for). The sibling only
    counts when its verified manifest has the same rollback target (this
    recovery target), database contract and images as the failed release, so
    it is the same upgrade from the same old release; nothing else is accepted."""
    source = parse_release(failed)[0]
    releases = {failed}
    for realm in REALMS:
        other = release_id(source, realm)
        if other == failed or not (state / "releases" / other).is_dir():
            continue
        sibling = verify_release(docker, state, secrets, other)[1]
        if (sibling.get("rollback_target") == rid and failed_manifest.get("rollback_target") == rid and
                sibling["database_contract_sha256"] == failed_manifest["database_contract_sha256"] and
                sibling["images"] == failed_manifest["images"]):
            releases.add(other)
    return releases


def upgrade_window_of(state, releases):
    """The (latest) upgrade window of the failed upgrade, current or archived."""
    releases = {releases} if isinstance(releases, str) else set(releases)
    found = []
    for path in [window_path(state), *sorted((state / "maintenance-history").glob("*.json"))]:
        if path.is_file():
            window = read_json(path)
            if window.get("release") in releases and window_kind(window) == "upgrade" and window.get("phases"):
                found.append(window)
    if not found:
        raise ValueError("找不到失败发布的升级维护窗口记录，无法确认升级前备份")
    return max(found, key=lambda w: w["phases"][0]["at"])


def check_backup_window(state, releases, meta, checksum):
    """CR-08B: the pre-upgrade backup is the one recorded in the failed
    release's upgrade window (FRESH_BACKUP_VERIFIED evidence carries its
    checksum) and was created inside that window, after writes stopped and
    before the backup phase; an older backup with the same schema is refused."""
    window = upgrade_window_of(state, releases)
    at = {e["phase"]: datetime.fromisoformat(e["at"]) for e in window["phases"]}
    if "FRESH_BACKUP_VERIFIED" not in at:
        raise ValueError("失败发布的升级窗口没有 FRESH_BACKUP_VERIFIED：没有可用的升级前备份")
    evidence = next(e["evidence"] for e in window["phases"] if e["phase"] == "FRESH_BACKUP_VERIFIED")
    if "sha256-" + checksum[:16] not in evidence:
        raise ValueError("该备份不是失败发布升级窗口中记录的升级前备份（FRESH_BACKUP_VERIFIED 证据中的校验和不同）")
    try:
        created = datetime.fromisoformat(str(meta.get("created_at", "")).replace("Z", "+00:00"))
    except ValueError as err:
        raise ValueError("备份清单缺少有效的创建时间") from err
    if not at["MAINTENANCE_ENTER"] <= created <= at["FRESH_BACKUP_VERIFIED"]:
        raise ValueError("备份不是在失败发布的维护窗口内（停止写入后）创建的")
    return window.get("id") or window_id(window)


def counts_sql(counts):
    return "SELECT json_build_object(%s)" % ",".join("'%s',(SELECT count(*) FROM %s)" % (t, t) for t in sorted(counts))


OWNERSHIP_SQL = """DO $$ DECLARE r record; BEGIN
 FOR r IN SELECT c.oid::regclass AS n FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'public' AND c.relkind IN ('r','p','v','m','f')
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
 LOOP EXECUTE format('ALTER TABLE %s OWNER TO baobei_migrator', r.n); END LOOP;
 FOR r IN SELECT c.oid::regclass AS n FROM pg_class c JOIN pg_namespace s ON s.oid = c.relnamespace
   WHERE s.nspname = 'public' AND c.relkind = 'S'
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('e','a','i'))
 LOOP EXECUTE format('ALTER SEQUENCE %s OWNER TO baobei_migrator', r.n); END LOOP;
 FOR r IN SELECT p.oid::regprocedure AS n FROM pg_proc p JOIN pg_namespace s ON s.oid = p.pronamespace
   WHERE s.nspname = 'public'
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
 LOOP EXECUTE format('ALTER ROUTINE %s OWNER TO baobei_migrator', r.n); END LOOP;
END $$"""


def recover(docker, state, secrets, rid, step, backup=None, now=None):
    if not sys.platform.startswith("linux"):
        raise ValueError("灾难恢复仅支持 Linux 部署宿主")
    phase = {"backup": "RECOVERY_BACKUP_VERIFIED", "stop": "APP_STOPPED", "restore": "DATABASE_RESTORED",
             "verify": "RECOVERY_VERIFIED", "start": "OLD_API_START"}.get(step)
    if not phase:
        raise ValueError("恢复步骤必须是 backup、stop、restore、verify 或 start")
    if step == "start":
        remaining = start_phases(state, rid, "disaster-recovery", "OLD_API_START")
        window = read_window(state)
    else:
        remaining = ()
        window = require_phase(state, rid, phase, kind="disaster-recovery")
    source, realm, target_release, target, first = recovery_context(docker, state, secrets, rid, window)
    ledger = expected_ledger(source, target)
    if step == "backup":
        failed = pointer(state, "pending") or pointer(state, "active")
        if not failed or failed == rid:
            raise ValueError("没有需要灾难恢复的失败发布（active/pending 已是目标旧版本）")
        failed_release, failed_manifest = verify_release(docker, state, secrets, failed)
        if failed_manifest.get("rollback_target") != rid:
            raise ValueError("目标不是失败发布清单记录的回滚基线")
        if failed_manifest["database_contract_sha256"] == target["database_contract_sha256"]:
            raise ValueError("数据库契约相同：使用普通 rollback，不需要灾难恢复")
        if backup is None:
            raise ValueError("需要 --backup 指定升级前备份文件")
        meta, checksum = verify_backup(backup, ledger)
        upgrade = upgrade_releases(docker, state, secrets, failed, failed_manifest, rid)
        upgrade_window = check_backup_window(state, upgrade, meta, checksum)
        return record_phase(state, rid, phase, "backup:sha256-" + checksum[:16], {
            "failed_release": failed, "backup": str(backup), "backup_sha256": checksum,
            "upgrade_window": upgrade_window, "upgrade_releases": sorted(upgrade),
            "backup_created_at": meta.get("created_at"), "ledger_rows": len(ledger),
            "counts_sha256": digest(canonical_json(meta["counts"]))}, kind="disaster-recovery")
    failed = first["failed_release"]
    failed_release, failed_manifest = verify_release(docker, state, secrets, failed)
    failed_realm = parse_release(failed)[1]
    images = failed_manifest["images"]
    if step == "stop":
        docker.compose(failed_release, secrets, images, "stop", "baobei-api", capture=False, realm=failed_realm)
        postgres = database_identity(docker, failed_release, secrets, images, failed_realm)
        drained = verify_runtime_drained(docker, failed_release, secrets, images, failed_realm)
        return record_phase(state, rid, phase, "stopped:" + postgres[0][:12],
                            {"runtime_drained": drained, "postgres": postgres[0][:12]})
    meta, checksum = verify_backup(first["backup"], ledger)
    if checksum != first["backup_sha256"]:
        raise ValueError("备份文件在恢复期间发生变化")
    postgres = database_identity(docker, failed_release, secrets, images, failed_realm)
    container = postgres_container(docker, failed_release, secrets, images, failed_realm)
    if step == "restore":
        verify_runtime_drained(docker, failed_release, secrets, images, failed_realm)
        current = database_facts(docker, container)
        swap = window.get("restore_swap")
        if (current["ledger"] == ledger and swap and swap.get("backup_sha256") == checksum and
                database_query(docker, container, "SELECT count(*) FROM pg_database WHERE datname = '%s'" %
                               swap["kept"], "postgres") == "1"):
            # Retry after an interruption that happened after this tool's own
            # swap (recorded before it ran): the live database already is the
            # verified restore; measure, do not restore a second time.
            measured = check_database_for(current, ledger, target, realm)
            if json.loads(database_query(docker, container, counts_sql(meta["counts"]))) != meta["counts"]:
                raise ValueError("业务数据库的迁移 ledger 已是旧版本但表计数与备份不一致：停止并人工核查")
            measured.update({"backup_sha256": checksum, "already_restored": True, "postgres": postgres[0][:12]})
            return record_phase(state, rid, phase, "restored:sha256-" + checksum[:16], measured)
        suffix = (now or datetime.now(timezone.utc)).strftime("%Y%m%d%H%M%S") + "_" + os.urandom(3).hex()
        scratch, kept = "baobei_restore_" + suffix, "baobei_failed_" + suffix
        grants = ",".join(APP_ROLES)
        # A scratch copy left by a killed earlier attempt is never live data.
        for name in database_query(docker, container, "SELECT coalesce(string_agg(datname, ' '), '') FROM pg_database "
                                   "WHERE datname ~ '^baobei_restore_[0-9]{14}_[0-9a-f]{6}$'", "postgres").split():
            database_query(docker, container, f"DROP DATABASE IF EXISTS {name} WITH (FORCE)", "postgres", read_only=False)
        database_query(docker, container, f"CREATE DATABASE {scratch}", "postgres", read_only=False)
        try:
            database_query(docker, container, f"REVOKE ALL ON DATABASE {scratch} FROM PUBLIC; "
                           f"GRANT CONNECT ON DATABASE {scratch} TO {grants}", scratch, read_only=False)
            with Path(first["backup"]).open("rb") as stream:
                docker.call("exec", "-i", container, "pg_restore", "-U", "baobei_bootstrap", "-d", scratch,
                            "--no-owner", "--exit-on-error", stdin=stream)
            database_query(docker, container, OWNERSHIP_SQL, scratch, read_only=False)
            measured = check_database_for(database_facts(docker, container, scratch), ledger, target, realm)
            if json.loads(database_query(docker, container, counts_sql(meta["counts"]), scratch)) != meta["counts"]:
                raise ValueError("恢复后的表计数与备份清单不一致")
            # One transaction: the verified copy becomes baobei; the failed
            # database is kept under another name without application access.
            # The intent is recorded first so a retry can tell this tool's swap
            # apart from a database that was never restored.
            window["restore_swap"] = {"kept": kept, "backup_sha256": checksum}
            atomic_json(window_path(state), window)
            database_query(docker, container, f"BEGIN; ALTER DATABASE {DATABASE} RENAME TO {kept}; "
                           f"ALTER DATABASE {scratch} RENAME TO {DATABASE}; "
                           f"REVOKE ALL ON DATABASE {kept} FROM PUBLIC, {grants}; COMMIT;", "postgres", read_only=False)
        except BaseException:
            database_query(docker, container, f"DROP DATABASE IF EXISTS {scratch} WITH (FORCE)", "postgres", read_only=False)
            raise
        measured.update({"backup_sha256": checksum, "failed_database_kept_as": kept, "postgres": postgres[0][:12]})
        return record_phase(state, rid, phase, "restored:sha256-" + checksum[:16], measured)
    facts = database_facts(docker, container)
    measured = check_database_for(facts, ledger, target, realm)
    if json.loads(database_query(docker, container, counts_sql(meta["counts"]))) != meta["counts"]:
        raise ValueError("业务数据库表计数与备份清单不一致")
    if step == "verify":
        measured.update({"backup_sha256": checksum, "postgres": postgres[0][:12]})
        return record_phase(state, rid, phase, "verified:" + measured["ledger_sha256"][:16], measured)
    # start: the old release on the restored database; pointers move last.
    check_mounts(secrets, "rollback", None, realm)
    if pointer(state, "pending") not in (None, failed):
        raise ValueError("另一提交有未完成部署")
    atomic_json(state / "pending.json", {"source_sha": failed})
    start(docker, target_release, secrets, target, realm)
    identity = running_identity(docker, target_release, secrets, target["images"], realm, target)
    identity.update(auth_probe(docker, target_release, secrets, target["images"], realm))
    check_database_for(database_facts(docker, container), ledger, target, realm)
    if "OLD_API_START" in remaining:
        record_phase(state, rid, "OLD_API_START", "image:" + target["images"]["baobei-api"]["id"][7:23], identity)
    if "WEB_START" in remaining:
        record_phase(state, rid, "WEB_START", "image:" + target["images"]["baobei-web"]["id"][7:23],
                     {"running_image": target["images"]["baobei-web"]["id"]})
    atomic_json(state / "active.json", {"source_sha": rid})
    (state / "pending.json").unlink(missing_ok=True)
    return read_window(state)


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
        if parse_release(target)[0] == source and parse_release(target)[1] != realm:
            raise ValueError("回滚目标是同一提交的另一 Realm：这是 Realm 切换，必须在 realm-switch 维护窗口内执行（不得用 rollback 绕过）")
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
            require_phase(state, rid, "SCHEMA_UPGRADE", kind="upgrade")
        remaining = ()
        if command == "up" and gated:
            remaining = start_phases(state, rid, "upgrade", "NEW_API_START")
        if command == "up" and same_commit_switch:
            # CR-08A: a same-commit realm switch is its own window kind, with the
            # measured baseline and the identity switch before the start.
            remaining = start_phases(state, rid, "realm-switch", "NEW_API_START")
        if command == "up" and (gated or same_commit_switch):
            check_precheck_secret(state, secrets, rid, realm)
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
        if gated or same_commit_switch:
            identity = {}
            if same_commit_switch:
                identity = running_identity(docker, release, secrets, images, realm, manifest)
                identity.update(auth_probe(docker, release, secrets, images, realm))
            if "NEW_API_START" in remaining:
                record_phase(state, rid, "NEW_API_START", "image:" + images["baobei-api"]["id"][7:23],
                             measured={"running_image": images["baobei-api"]["id"], **identity})
            if "WEB_START" in remaining:
                record_phase(state, rid, "WEB_START", "image:" + images["baobei-web"]["id"][7:23],
                             measured={"running_image": images["baobei-web"]["id"]})
        atomic_json(state / "active.json", {"source_sha": rid})
    (state / "pending.json").unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("prepare", "db", "migrate", "up", "rollback", "phase", "status",
                                            "preflight", "switch", "switch-baseline", "recover", "abort-window"))
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--identity-realm", choices=tuple(REALMS), default=DEFAULT_REALM,
                        help="批准的身份 Realm；默认 crane（现状）。写入不可变发布清单")
    parser.add_argument("--confirm-realm", choices=tuple(REALMS),
                        help="切换身份 Realm 的发布或回滚时，明确确认目标 Realm（须已完成 identity-switch）")
    parser.add_argument("--state-dir", type=Path, required=True)
    parser.add_argument("--secrets-dir", type=Path, required=True)
    parser.add_argument("--migrator-dsn", type=Path)
    parser.add_argument("--phase", choices=ALL_PHASES)
    parser.add_argument("--window-kind", choices=tuple(WINDOW_KINDS), default="upgrade",
                        help="MAINTENANCE_ENTER 时选择维护窗口类型（CR-08A）")
    parser.add_argument("--evidence")
    parser.add_argument("--step", choices=("backup", "stop", "restore", "verify", "start"),
                        help="recover：灾难恢复步骤（CR-08B）")
    parser.add_argument("--backup", type=Path, help="recover --step backup：升级前备份文件（带 .json 清单）")
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
            window = read_window(state)
            kind = args.window_kind if args.phase == "MAINTENANCE_ENTER" or not window else window_kind(window)
            operator_phase_allowed(args.phase, args.identity_realm, kind)
            window = record_phase(state, rid, args.phase, args.evidence, kind=kind)
            print("BAOBEI_PHASE_" + args.phase + "=RECORDED " + str(len(window["phases"])) + "/" +
                  str(len(window_phases(window))) + " kind=" + window_kind(window))
            return
        if args.command == "status":
            print(json.dumps(status_report(state), ensure_ascii=False, sort_keys=True))
            return
        if args.command == "abort-window":
            window = abort_window(state, rid, args.evidence)
            print("BAOBEI_WINDOW=ABORTED next_phase=%s pending=%s" % (window["aborted"]["next_phase"],
                                                                    window["aborted"]["pending"]))
            return
        if args.command == "preflight":
            window = preflight(state, rid, args.takeover_admin, args.change, {
                "base": args.keycloak_base, "admin": args.admin_row, "link": args.link_row,
                "service_secret_file": args.service_secret_file, "password_file": args.password_file,
                "web_secret_file": args.web_secret_file.absolute() if args.web_secret_file else None,
                "redirect": args.redirect}, secrets=secrets)
            m = window["phases"][-1]["measured"]
            print("BAOBEI_PREFLIGHT=PASS realm=%s admin=%s expires_at=%s" % (m["realm"], m["staff_id"], m["expires_at"]))
            return
        docker = Docker()
        if args.command == "switch":
            identity_switch(docker, state, secrets, rid, args.takeover_admin, args.operator, args.reason, args.approval,
                            args.maintenance_dsn.absolute() if args.maintenance_dsn else None)
            print("BAOBEI_IDENTITY_SWITCH=PASS")
            return
        if args.command == "switch-baseline":
            realm_switch_baseline(docker, state, secrets, rid)
            print("BAOBEI_SWITCH_BASELINE=PASS")
            return
        if args.command == "recover":
            recover(docker, state, secrets, rid, args.step, args.backup.absolute() if args.backup else None)
            print("BAOBEI_RECOVER_" + (args.step or "").upper() + "=PASS")
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
