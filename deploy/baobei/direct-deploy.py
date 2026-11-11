#!/usr/bin/env python3
"""Build and deploy Baobei from a reviewed product commit using local Docker images."""

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
from hashlib import sha256
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
    "compose.production.yaml", "compose.direct.yaml", "nginx.production.conf",
    "nginx.proxy.production.conf", "db/initialize.sql", "db/staff-role.sql",
)
SECRET_NAMES = ("db_password", "runtime_database_url", "staff_database_url", "oidc_client_secret")
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


def export_source(source, dest):
    """Extract only regular tracked build inputs and runtime configuration."""
    selected = {"deploy/baobei/" + name for name in SNAPSHOT}
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

    def compose(self, release, secrets, images, *args, capture=True):
        env = {
            "COMPOSE_DISABLE_ENV_FILE": "1",
            "BAOBEI_SECRETS_DIR": str(secrets),
            "BAOBEI_WEB_IMAGE": images["baobei-web"]["ref"],
            "BAOBEI_API_IMAGE": images["baobei-api"]["ref"],
        }
        return self.call("compose", "--project-name", "u-dao-baobei",
                         "-f", "compose.production.yaml", "-f", "compose.direct.yaml",
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


def config_hash(docker, release, secrets, images):
    config = json.loads(docker.compose(release, secrets, images, "config", "--no-path-resolution",
                                      "--format", "json"))
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
    if not SHA.fullmatch(source):
        raise ValueError("部署状态指针损坏")
    return source


def verify_release(docker, state, secrets, source):
    if not SHA.fullmatch(source):
        raise ValueError("无效的部署提交 SHA")
    release = state / "releases" / source
    require_directory(release)
    manifest = read_json(release / "manifest.json")
    if (manifest["mode"] != "direct-local" or manifest["source_sha"] != source or
            manifest["source"] != SOURCE or manifest["platform"] != docker.platform or
            manifest["snapshot_sha256"] != hash_files(release, SNAPSHOT)):
        raise ValueError("部署清单与当前 Docker 或 Compose 配置不一致")
    if set(manifest["images"]) != set(SERVICES):
        raise ValueError("清单必须包含 Web 和 API 镜像")
    for service, image in manifest["images"].items():
        if docker.inspect(local_ref(service, source), source) != image:
            raise ValueError("本地镜像已更改，请停止部署")
    if manifest["compose_sha256"] != config_hash(docker, release, secrets, manifest["images"]):
        raise ValueError("Compose 配置哈希不匹配")
    return release, manifest


def prepare(docker, state, secrets, source):
    verify_source(source)
    go_proxy = os.environ.get("BAOBEI_GOPROXY", PUBLIC_GO_PROXIES[0])
    if go_proxy not in PUBLIC_GO_PROXIES:
        raise ValueError("BAOBEI_GOPROXY 必须使用规定的无凭据公共地址")
    releases = state / "releases"
    releases.mkdir(exist_ok=True)
    require_directory(releases)
    release = releases / source
    if release.exists():
        verify_release(docker, state, secrets, source)
        return release
    with tempfile.TemporaryDirectory(dir=state, prefix="build-") as tmp:
        staged = Path(tmp)
        export_source(source, staged)
        snapshot = staged / "snapshot"
        snapshot.mkdir()
        for name in SNAPSHOT:
            dest = snapshot / name
            dest.parent.mkdir(exist_ok=True)
            shutil.copyfile(staged / "deploy/baobei" / name, dest)
        db_contract = database_contract(staged)
        images = build_images(docker, source, staged, go_proxy)
        manifest = {
            "mode": "direct-local", "source_sha": source, "source": SOURCE, "platform": docker.platform,
            "built_at": datetime.now(timezone.utc).isoformat(), "images": images, "go_proxy": go_proxy,
            "compose_sha256": config_hash(docker, snapshot, secrets, images),
            "snapshot_sha256": hash_files(snapshot, SNAPSHOT), "database_contract_sha256": db_contract,
            "rollback_target": pointer(state, "active"),
        }
        atomic_json(snapshot / "manifest.json", manifest)
        snapshot.rename(release)
    return release


def check_mounts(secrets, command, migrator_dsn):
    require_directory(secrets)
    names = ("db_password",) if command == "db" else SECRET_NAMES if command in ("up", "rollback") else ()
    for name in names:
        require_file(secrets / name)
    if command in ("up", "rollback"):
        require_directory(WEBROOT)
    if command == "migrate":
        if migrator_dsn is None or migrator_dsn.parent != secrets:
            raise ValueError("需要在外部密钥目录提供 --migrator-dsn")
        require_file(migrator_dsn)


def database_identity(docker, release, secrets, images):
    """Return the running PostgreSQL container identity; refuse if absent or unhealthy."""
    container = docker.compose(release, secrets, images, "ps", "-q", "baobei-postgres").decode().strip()
    if not container or len(container.split()) != 1:
        raise ValueError("PostgreSQL 未运行，拒绝切换应用；首次安装须先执行 db 步骤")
    state = docker.call("inspect", "--format",
                        "{{.Id}} {{.State.StartedAt}} {{if .State.Health}}{{.State.Health.Status}}{{end}}",
                        container).decode().split()
    if len(state) != 3 or state[2] != "healthy":
        raise ValueError("PostgreSQL 不健康，拒绝切换应用")
    return state[0], state[1]


def start(docker, release, secrets, manifest):
    """Reconcile only Web/API. PostgreSQL is created solely by the explicit db command."""
    images = manifest["images"]
    database = database_identity(docker, release, secrets, images)
    docker.compose(release, secrets, images, *APP_UP, capture=False)
    if database_identity(docker, release, secrets, images) != database:
        raise ValueError("PostgreSQL 容器在应用切换期间被改变，停止并人工核查")
    for service, image in images.items():
        container = docker.compose(release, secrets, images, "ps", "-q", service).decode().strip()
        if not container or docker.call("inspect", "--format", "{{.Image}}", container).decode().strip() != image["id"]:
            raise ValueError("运行容器的镜像 ID 与部署清单不匹配")


def apply(docker, state, secrets, source, command, migrator_dsn):
    if not sys.platform.startswith("linux"):
        raise ValueError("启动、迁移和回滚仅支持 Linux 部署宿主")
    check_mounts(secrets, command, migrator_dsn)
    release, manifest = verify_release(docker, state, secrets, source)
    active, pending = pointer(state, "active"), pointer(state, "pending")
    if pending not in (None, source):
        raise ValueError("另一提交有未完成部署，先恢复该提交")
    images = manifest["images"]
    if command == "rollback":
        target = manifest["rollback_target"]
        if not target or source not in (active, pending):
            raise ValueError("没有本次部署的可用回滚目标")
        release, previous = verify_release(docker, state, secrets, target)
        if manifest["database_contract_sha256"] != previous["database_contract_sha256"]:
            raise ValueError("数据库契约不同，需要经审查的数据库恢复流程")
        atomic_json(state / "pending.json", {"source_sha": source})
        start(docker, release, secrets, previous)
        atomic_json(state / "active.json", {"source_sha": target})
    else:
        if active not in (source, manifest["rollback_target"]):
            raise ValueError("清单的回滚基线已过期")
        atomic_json(state / "pending.json", {"source_sha": source})
        if command == "db":
            docker.compose(release, secrets, images, *DB_UP, capture=False)
            return
        if command == "migrate":
            docker.call("run", "--rm", "--pull", "never", "--read-only", "--network", "u-dao-baobei-internal",
                        "--security-opt", "no-new-privileges", "--env", "APP_ENV=production",
                        "--env", "DATABASE_URL_FILE=/run/secrets/db_url",
                        "--mount", f"type=bind,src={migrator_dsn},dst=/run/secrets/db_url,readonly",
                        images["baobei-api"]["id"], "maintenance", "migrate", capture=False)
            return
        start(docker, release, secrets, manifest)
        atomic_json(state / "active.json", {"source_sha": source})
    (state / "pending.json").unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("prepare", "db", "migrate", "up", "rollback"))
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--state-dir", type=Path, required=True)
    parser.add_argument("--secrets-dir", type=Path, required=True)
    parser.add_argument("--migrator-dsn", type=Path)
    args = parser.parse_args()
    state, secrets = args.state_dir.absolute(), args.secrets_dir.absolute()
    if (state.is_relative_to(ROOT) or secrets.is_relative_to(ROOT) or secrets.is_relative_to(state) or
            state.is_relative_to(secrets)):
        raise ValueError("状态与密钥目录必须彼此独立并位于源码工作树之外")
    private_directory(state)
    with deployment_lock(state):
        docker = Docker()
        if args.command == "prepare":
            path = prepare(docker, state, secrets, args.source_sha)
            print(path / "manifest.json")
        else:
            apply(docker, state, secrets, args.source_sha, args.command,
                  args.migrator_dsn.absolute() if args.migrator_dsn else None)
            print("BAOBEI_DIRECT_" + args.command.upper() + "=PASS")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, TypeError) as error:
        sys.exit(str(error))
