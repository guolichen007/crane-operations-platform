#!/usr/bin/env python3
"""Baobei V1.1 production go-live initialization (one-time, operator only).

Builds a clean production database for the go-live: the exact live schema
(every migration, grant, immutable trigger, the environment marker if any and
the identity epoch) with only the approved people (whitelist), one sales team
and no business data. The live database is never modified, no row is deleted
from it and no trigger is disabled: the clean database is built next to it,
verified, and swapped in by one rename transaction while the API is stopped.
The old database is kept (renamed, no application access) for rollback.

There is no network or HTTP interface; this runs on the deployment host as the
operator, against the Baobei PostgreSQL container only (docker exec, bootstrap
role over the container's local socket).

Steps (each needs the private state directory; every write needs --approval):
  inventory  read-only dry run: current data, the whitelist check and the plan
  backup     full backup of the live database (pg_dump -Fc, sha256, ledger,
             counts) and a restore check into a disposable database
  build      create and verify the clean database (live stays untouched)
  swap       API stopped, no business connection: rename live -> kept,
             clean -> baobei in one transaction
  verify     re-verify the live database after the swap
  rollback   API stopped: rename the clean database away and the kept one back

Whitelist (private 0600 JSON):
  {"approval": "OWNER-...", "team": {"name": "销售一组"},
   "keep":   [{"staff_id": "<uuid>", "role": "baobei_admin"}],
   "create": [{"name": "备用管理员", "role": "baobei_admin", "claim_login": "admin02"},
              {"name": "销售01", "role": "baobei_sales", "claim_login": "sales01"}, ...]}
  keep   : existing members kept exactly (staff id, subject, identity link,
           phone, role); must be active, linked under the trusted issuer.
  create : new members waiting for the first-time claim (00016): the
           administrator opens their login from the web page; each member
           enters their own name and phone at the first sign-in.
"""
import argparse
from datetime import datetime, timezone
from hashlib import sha256
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

HERE = Path(__file__).resolve().parent
_spec = importlib.util.spec_from_file_location("baobei_direct_deploy", HERE / "direct-deploy.py")
direct = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(direct)

DATABASE = "baobei"
CONTAINER = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}")
UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
CLAIM = re.compile(r"[a-z][a-z0-9_-]{2,31}")
APPROVAL = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:@/+-]{2,199}")
ROLES = ("baobei_sales", "baobei_manager", "baobei_director", "baobei_admin")
SCRATCH = re.compile(r"baobei_golive_[0-9]{14}_[0-9a-f]{6}")
FORMAT = "baobei-golive-backup-v1"

# Every table of the schema has exactly one rule. A table the tool does not
# know (a later migration) stops the tool instead of being copied or dropped.
KEEP_ALL = ("baobei_migrations", "identity_epoch", "reference_values", "baobei_environment")
FILTERED = ("teams", "app_users", "identity_links", "staff_audit", "profile_audit")
EMPTY = (
    "account_operations", "app_sessions", "approvals", "audit_events", "contacts", "conversion_jobs",
    "conversion_links", "customer_audit", "duplicate_cases", "intake_projects", "oidc_pending_logins",
    "organization_aliases", "organization_team_profiles", "organizations", "ownership_history",
    "planned_equipment", "project_activities", "project_contacts", "project_create_requests",
    "project_locations", "project_participations", "project_parties", "project_requirements",
    "project_review_routes", "project_submissions", "project_technical_brief_revisions",
    "project_technical_briefs", "project_technical_question_events", "project_technical_questions",
    "registration_counters", "staff_handoffs", "staff_outbox", "staff_sessions",
)
TABLES_SQL = "SELECT coalesce(json_agg(tablename ORDER BY tablename),'[]') FROM pg_tables WHERE schemaname='public'"
# Schema fingerprint: tables/columns, constraints, indexes, triggers (enabled
# state included), functions with their definitions, extensions, grants.
SCHEMA_SQL = """SELECT json_build_object(
 'columns', (SELECT json_agg(x ORDER BY x) FROM (SELECT table_name||'.'||column_name||':'||data_type||':'||is_nullable||':'||coalesce(column_default,'') x
   FROM information_schema.columns WHERE table_schema='public') c),
 'constraints', (SELECT json_agg(x ORDER BY x) FROM (SELECT conrelid::regclass::text||':'||conname||':'||pg_get_constraintdef(oid) x
   FROM pg_constraint WHERE connamespace='public'::regnamespace) c),
 'indexes', (SELECT json_agg(x ORDER BY x) FROM (SELECT indexdef x FROM pg_indexes WHERE schemaname='public') i),
 'triggers', (SELECT json_agg(x ORDER BY x) FROM (SELECT tgrelid::regclass::text||':'||tgname||':'||tgenabled::text||':'||pg_get_triggerdef(oid) x
   FROM pg_trigger WHERE NOT tgisinternal) t),
 'functions', (SELECT json_agg(x ORDER BY x) FROM (SELECT p.oid::regprocedure::text||':'||md5(pg_get_functiondef(p.oid))||':'||pg_get_userbyid(p.proowner)||':'||coalesce(p.proacl::text,'') x
   FROM pg_proc p WHERE p.pronamespace='public'::regnamespace AND p.prokind='f'
   AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e')) f),
 'extensions', (SELECT json_agg(extname||':'||extversion ORDER BY extname) FROM pg_extension),
 'grants', (SELECT json_agg(x ORDER BY x) FROM (SELECT c.relname||':'||coalesce(c.relacl::text,'')||':'||pg_get_userbyid(c.relowner) x
   FROM pg_class c WHERE c.relnamespace='public'::regnamespace AND c.relkind IN ('r','S','v')) g),
 'column_grants', (SELECT json_agg(x ORDER BY x) FROM (SELECT table_name||'.'||column_name||':'||grantee||':'||privilege_type x
   FROM information_schema.column_privileges WHERE table_schema='public' AND grantee LIKE 'baobei\\_%') g)
)"""


class Refused(Exception):
    pass


def now_tag():
    return datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S") + "_" + os.urandom(3).hex()


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def digest(value):
    return sha256(canonical(value)).hexdigest()


def private_file(path):
    path = Path(path)
    if not path.is_absolute() or not path.is_file() or path.resolve() != path:
        raise Refused(f"{path} 必须是已存在的绝对路径普通文件")
    if os.name != "nt" and path.stat().st_mode & (stat.S_IRWXG | stat.S_IRWXO):
        raise Refused(f"{path.name} 必须是仅本人可读的私有文件（chmod 600）")
    return path


def private_dir(path):
    path = Path(path)
    if not path.is_absolute() or not path.is_dir() or path.resolve() != path:
        raise Refused("状态目录必须是已存在的绝对路径目录")
    if os.name != "nt" and (path.stat().st_uid != os.geteuid() or path.stat().st_mode & 0o077):
        raise Refused("状态目录必须由当前操作者拥有，权限 0700")
    if path.is_relative_to(direct.ROOT):
        raise Refused("状态目录不得位于源码工作树内")
    return path


def mask(phone):
    return phone[:-8] + "****" + phone[-4:] if phone and len(phone) >= 8 else ""


class Psql:
    """PostgreSQL access as the bootstrap role. Docker: the Baobei container's
    local socket (production). Local: a loopback server, for drills only."""

    def __init__(self, container=None, local=None):
        if container:
            if not CONTAINER.fullmatch(container):
                raise Refused("容器名无效")
            labels = json.loads(subprocess.run(["docker", "inspect", "--format", "{{json .Config.Labels}}", container],
                                               check=True, capture_output=True).stdout)
            if (labels.get("com.docker.compose.project") != "u-dao-baobei" or
                    labels.get("com.docker.compose.service") != "baobei-postgres"):
                raise Refused("只能对 u-dao-baobei 的 baobei-postgres 容器执行")
            self.prefix = ["docker", "exec", "-i", container]
            self.conn = ["-U", "baobei_bootstrap"]
            self.container = container
        else:
            host, port = local
            if host not in ("127.0.0.1", "localhost") or os.environ.get("BAOBEI_GOLIVE_DRILL") != "1":
                raise Refused("本地模式只用于隔离演练（127.0.0.1 且 BAOBEI_GOLIVE_DRILL=1）")
            self.prefix = []
            self.conn = ["-h", host, "-p", str(port), "-U", os.environ.get("BAOBEI_GOLIVE_DRILL_USER", "baobei_bootstrap")]
            self.container = None

    def _cmd(self, tool, database, *args):
        if not direct.SAFE_NAME.fullmatch(database):
            raise Refused("数据库名无效")
        return [*self.prefix, tool, *self.conn, "-d", database, *args]

    def run(self, database, sql, write=False):
        body = sql if write else "BEGIN READ ONLY; " + sql + "; COMMIT;"
        r = subprocess.run(self._cmd("psql", database, "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-c", body),
                           capture_output=True)
        if r.returncode:
            raise Refused("数据库命令失败：" + r.stderr.decode(errors="replace").strip()[-400:])
        return r.stdout.decode().strip()

    def json(self, database, sql):
        return json.loads(self.run(database, sql) or "null")

    def copy(self, source, select, target, table, columns):
        out = subprocess.Popen(self._cmd("psql", source, "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-c",
                                         f"COPY ({select}) TO STDOUT"), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        load = subprocess.run(self._cmd("psql", target, "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-c",
                                        f"COPY {table}({columns}) FROM STDIN"), stdin=out.stdout, capture_output=True)
        out.stdout.close()
        err = out.stderr.read()
        if out.wait() or load.returncode:
            raise Refused(f"复制 {table} 失败：" + (err + load.stderr).decode(errors="replace").strip()[-400:])

    def schema_into(self, source, target):
        dump = subprocess.run(self._cmd("pg_dump", source, "--schema-only", "--no-comments"), capture_output=True)
        if dump.returncode:
            raise Refused("导出结构失败：" + dump.stderr.decode(errors="replace")[-400:])
        load = subprocess.run(self._cmd("psql", target, "-X", "-q", "-v", "ON_ERROR_STOP=1", "-f", "-"),
                              input=dump.stdout, capture_output=True)
        if load.returncode:
            raise Refused("导入结构失败：" + load.stderr.decode(errors="replace")[-400:])

    def dump(self, database, path):
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "wb") as out:
            r = subprocess.run(self._cmd("pg_dump", database, "-Fc"), stdout=out, stderr=subprocess.PIPE)
        if r.returncode:
            raise Refused("备份失败：" + r.stderr.decode(errors="replace")[-400:])

    def restore(self, database, path):
        with open(path, "rb") as stream:
            r = subprocess.run(self._cmd("pg_restore", database, "--exit-on-error"), stdin=stream, capture_output=True)
        if r.returncode:
            raise Refused("恢复失败：" + r.stderr.decode(errors="replace")[-400:])


def tables(db, database):
    names = db.json(database, TABLES_SQL)
    known = set(KEEP_ALL) | set(FILTERED) | set(EMPTY)
    unknown = sorted(set(names) - known)
    if unknown:
        raise Refused("存在初始化工具未分类的表（新迁移？）：" + ", ".join(unknown))
    for required in ("baobei_migrations", "identity_epoch", "app_users", "teams", "identity_links"):
        if required not in names:
            raise Refused("数据库缺少必要表 " + required)
    return names


def counts(db, database, names):
    return db.json(database, "SELECT json_build_object(%s)" % ",".join(f"'{t}',(SELECT count(*) FROM {t})" for t in names))


def epoch(db, database):
    return db.json(database, "SELECT json_build_object('issuer',issuer,'epoch',epoch) FROM identity_epoch")


def load_whitelist(path):
    raw = json.loads(private_file(path).read_bytes())
    if set(raw) - {"approval", "team", "keep", "create"} or not APPROVAL.fullmatch(str(raw.get("approval", ""))):
        raise Refused("白名单字段无效或缺少 approval")
    team = raw.get("team") or {}
    name = str(team.get("name", "")).strip()
    if set(team) - {"name"} or not name or len(name) > 64:
        raise Refused("白名单必须且只能有一个团队名称")
    keep, create = raw.get("keep") or [], raw.get("create") or []
    seen_ids, seen_logins = set(), set()
    for k in keep:
        if set(k) != {"staff_id", "role"} or not UUID.fullmatch(str(k["staff_id"])) or k["role"] not in ROLES or k["staff_id"] in seen_ids:
            raise Refused("keep 项无效：需要唯一的 staff_id 与角色")
        seen_ids.add(k["staff_id"])
    for c in create:
        if set(c) != {"name", "role", "claim_login"} or c["role"] not in ROLES or not CLAIM.fullmatch(str(c["claim_login"])):
            raise Refused("create 项无效：需要姓名、角色与临时登录名（小写字母开头 3–32 位）")
        n = str(c["name"]).strip()
        if not n or len(n) > 64 or re.fullmatch(r"[0-9\s]+", n) or c["claim_login"] in seen_logins:
            raise Refused("create 项姓名无效或临时登录名重复")
        seen_logins.add(c["claim_login"])
    if not any(k["role"] == "baobei_admin" for k in keep):
        raise Refused("至少保留一名现有管理员（OWNER）")
    return {"approval": raw["approval"], "team": name, "keep": keep, "create": create,
            "sha256": sha256(canonical(raw)).hexdigest()}


MEMBER_SQL = """SELECT coalesce(json_agg(json_build_object('staff_id',u.staff_id,'subject',u.subject,'role',u.role,
 'team_id',u.team_id,'status',u.staff_status,'active',u.active,'account_state',u.account_state,'phone',coalesce(u.work_phone,''),
 'name',u.display_name,'claim_state',u.claim_state,
 'links',(SELECT coalesce(json_agg(json_build_object('issuer',l.issuer,'external',l.external_subject) ORDER BY l.issuer),'[]')
   FROM identity_links l WHERE l.subject=u.subject)) ORDER BY u.staff_id),'[]') FROM app_users u"""


def check_keep(members, wl, trusted):
    by_id = {m["staff_id"]: m for m in members}
    kept = []
    for k in wl["keep"]:
        m = by_id.get(k["staff_id"])
        if not m:
            raise Refused("保留成员不存在：" + k["staff_id"])
        problems = []
        if m["role"] != k["role"]:
            problems.append("角色与白名单不一致")
        if m["status"] != "ACTIVE" or not m["active"] or m["account_state"] != "READY":
            problems.append("不是在用且可登录的账号")
        if not any(l["issuer"] == trusted["issuer"] for l in m["links"]):
            problems.append("没有当前可信 Issuer 的身份链接")
        if not m["phone"]:
            problems.append("没有正式手机号")
        if m["role"] in ("baobei_admin", "baobei_director") and m["team_id"]:
            problems.append("管理员/总监不应带团队")
        if m["role"] in ("baobei_sales", "baobei_manager"):
            problems.append("保留的销售/主管会失去原团队，请改为新建成员")
        if problems:
            raise Refused("保留成员 %s 不合规：%s" % (k["staff_id"], "；".join(problems)))
        kept.append(m)
    return kept


def plan(db, wl):
    names = tables(db, DATABASE)
    live_counts = counts(db, DATABASE, names)
    trusted = epoch(db, DATABASE)
    if not trusted:
        raise Refused("数据库没有身份纪元")
    members = db.json(DATABASE, MEMBER_SQL)
    kept = check_keep(members, wl, trusted)
    taken = set(db.json(DATABASE, "SELECT coalesce(json_agg(claim_login),'[]') FROM app_users WHERE claim_login IS NOT NULL"))
    return {
        "tables": names, "live_counts": live_counts, "trusted": trusted,
        "members_total": len(members), "kept": kept,
        "dropped_members": len(members) - len(kept),
        "dropped_links": [l["external"] for m in members if m["staff_id"] not in {k["staff_id"] for k in kept}
                          for l in m["links"] if l["issuer"] == trusted["issuer"]],
        "reused_claim_logins": sorted(taken & {c["claim_login"] for c in wl["create"]}),
    }


def show_plan(p, wl):
    return {
        "approval": wl["approval"], "whitelist_sha256": wl["sha256"], "issuer": p["trusted"]["issuer"],
        "epoch": p["trusted"]["epoch"], "migrations": p["live_counts"]["baobei_migrations"],
        "live_counts": p["live_counts"],
        "keep": [{"staff_id": m["staff_id"], "role": m["role"], "phone": mask(m["phone"]), "name": m["name"]} for m in p["kept"]],
        "create": [{"role": c["role"], "claim_login": c["claim_login"], "name": c["name"]} for c in wl["create"]],
        "team": wl["team"], "members_dropped": p["dropped_members"],
        "keycloak_users_to_disable_after_go_live": len(p["dropped_links"]),
        "business_tables_emptied": sorted(t for t in p["tables"] if t in EMPTY and p["live_counts"][t]),
    }


def state_file(state):
    return state / "golive.json"


def read_state(state):
    path = state_file(state)
    return json.loads(path.read_bytes()) if path.exists() else {}


def write_state(state, value):
    direct.atomic_json(state_file(state), value)
    os.chmod(state_file(state), 0o600)


def backup(db, state, wl):
    names = tables(db, DATABASE)
    tag = now_tag()
    archive = state / f"baobei-golive-{tag}.dump"
    ledger = db.json(DATABASE, direct.LEDGER_SQL)
    before = counts(db, DATABASE, names)
    db.dump(DATABASE, archive)
    if counts(db, DATABASE, names) != before:
        raise Refused("备份期间数据发生变化：请先停止 API 再备份")
    h = sha256()
    with archive.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    meta = {"format": FORMAT, "database": DATABASE, "created_at": datetime.now(timezone.utc).isoformat(),
            "sha256": h.hexdigest(), "migrations": ledger, "counts": before, "approval": wl["approval"]}
    side = Path(str(archive) + ".json")
    fd = os.open(side, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as out:
        out.write(canonical(meta))
    # Restore check into a disposable database (never the live one).
    check = "baobei_golive_check_" + tag.split("_")[1]
    db.run("postgres", f"CREATE DATABASE {check}", write=True)
    try:
        db.restore(check, archive)
        if counts(db, check, names) != before or db.json(check, direct.LEDGER_SQL) != ledger:
            raise Refused("备份恢复校验不一致")
    finally:
        db.run("postgres", f"DROP DATABASE IF EXISTS {check} WITH (FORCE)", write=True)
    st = read_state(state)
    st["backup"] = {"archive": str(archive), "sha256": meta["sha256"], "counts": before, "restore_check": "PASS",
                    "created_at": meta["created_at"]}
    write_state(state, st)
    return {"BACKUP": "PASS", "archive": str(archive), "sha256": meta["sha256"], "restore_check": "PASS"}


def require_backup(db, state):
    st = read_state(state)
    b = st.get("backup")
    if not b:
        raise Refused("没有本次初始化的完整备份：先执行 backup")
    archive = Path(b["archive"])
    h = sha256()
    with archive.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            h.update(chunk)
    if h.hexdigest() != b["sha256"]:
        raise Refused("备份文件已变化")
    if counts(db, DATABASE, tables(db, DATABASE)) != b["counts"]:
        raise Refused("备份之后线上数据已变化：请重新备份")
    return b


def build(db, state, wl, operator):
    p = plan(db, wl)
    require_backup(db, state)
    names = p["tables"]
    for leftover in (db.run("postgres", "SELECT coalesce(string_agg(datname,' '),'') FROM pg_database "
                            "WHERE datname ~ '^baobei_golive_[0-9]{14}_[0-9a-f]{6}$'") or "").split():
        st = read_state(state)
        if leftover in (st.get("swap") or {}).values():
            raise Refused("已执行过切换：不能再次构建（如需回退使用 rollback）")
        db.run("postgres", f"DROP DATABASE IF EXISTS {leftover} WITH (FORCE)", write=True)
    scratch = "baobei_golive_" + now_tag()
    roles = ",".join(direct.APP_ROLES)
    db.run("postgres", f"CREATE DATABASE {scratch}", write=True)
    try:
        db.run(scratch, f"REVOKE ALL ON DATABASE {scratch} FROM PUBLIC; GRANT CONNECT ON DATABASE {scratch} TO {roles}", write=True)
        db.schema_into(DATABASE, scratch)
        cols = lambda t: db.run(DATABASE, f"SELECT string_agg(quote_ident(column_name),',' ORDER BY ordinal_position) "
                                          f"FROM information_schema.columns WHERE table_schema='public' AND table_name='{t}'")
        for t in KEEP_ALL:
            if t in names:
                db.copy(DATABASE, f"SELECT {cols(t)} FROM {t}", scratch, t, cols(t))
        kept_ids = ",".join(f"'{m['staff_id']}'" for m in p["kept"])
        kept_subjects = ",".join("'" + m["subject"].replace("'", "''") + "'" for m in p["kept"])
        db.copy(DATABASE, f"SELECT {cols('app_users')} FROM app_users WHERE staff_id IN ({kept_ids})", scratch, "app_users", cols("app_users"))
        db.copy(DATABASE, f"SELECT {cols('identity_links')} FROM identity_links WHERE subject IN ({kept_subjects})",
                scratch, "identity_links", cols("identity_links"))
        db.copy(DATABASE, f"SELECT {cols('staff_audit')} FROM staff_audit WHERE target_id IN ({kept_ids})", scratch, "staff_audit", cols("staff_audit"))
        db.copy(DATABASE, f"SELECT {cols('profile_audit')} FROM profile_audit WHERE staff_id IN ({kept_ids})", scratch, "profile_audit", cols("profile_audit"))
        actor = "ops:golive-init:" + operator
        team = wl["team"].replace("'", "''")
        lines = [f"SELECT set_config('baobei.actor', '{actor}', true)",
                 f"INSERT INTO teams(name) VALUES ('{team}')"]
        for m in p["kept"]:
            lines.append(f"INSERT INTO staff_audit(target_id,actor,action,reason,version) SELECT staff_id,'{actor}','staff.golive-keep',"
                         f"'正式投产初始化：保留身份 · {wl['approval']}',staff_version FROM app_users WHERE staff_id='{m['staff_id']}'")
        for c in wl["create"]:
            name = c["name"].strip().replace("'", "''")
            team_expr = "(SELECT id FROM teams)" if c["role"] in ("baobei_sales", "baobei_manager") else "NULL"
            lines.append(
                "WITH id AS (SELECT gen_random_uuid() AS v) INSERT INTO app_users(staff_id,subject,display_name,role,team_id,active,"
                "staff_status,staff_note,staff_permissions,staff_managed,claim_login,claim_state) "
                f"SELECT v,'pending:'||v::text,'{name}','{c['role']}',{team_expr},false,'PENDING_PROVISION','正式投产初始化',7,true,"
                f"'{c['claim_login']}','PENDING' FROM id")
            lines.append(f"INSERT INTO staff_audit(target_id,actor,action,reason,version) SELECT staff_id,'{actor}','staff.invite',"
                         f"'正式投产初始化 · 临时登录名 {c['claim_login']} · {wl['approval']}',1 FROM app_users WHERE claim_login='{c['claim_login']}'")
        db.run(scratch, "BEGIN; " + "; ".join(lines) + "; COMMIT;", write=True)
        measured = verify_clean(db, scratch, p, wl)
    except BaseException:
        db.run("postgres", f"DROP DATABASE IF EXISTS {scratch} WITH (FORCE)", write=True)
        raise
    # Keycloak users of members that do not enter the clean database (UAT and
    # test accounts): ids only, for the operator to disable/delete in realm
    # baobei after the swap (otherwise their phone usernames stay occupied).
    dropped = state / "dropped-keycloak-users.json"
    fd = os.open(dropped, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "wb") as out:
        out.write(canonical({"issuer": p["trusted"]["issuer"], "keycloak_user_ids": sorted(p["dropped_links"])}))
    st = read_state(state)
    st["build"] = {"database": scratch, "measured": measured, "whitelist_sha256": wl["sha256"], "approval": wl["approval"],
                   "at": datetime.now(timezone.utc).isoformat()}
    write_state(state, st)
    return {"BUILD": "PASS", "database": scratch, **measured}


def verify_clean(db, database, p, wl):
    """The clean database: same schema, ledger, epoch and privileges as the
    live one; only the approved people and one team; no business data."""
    problems = []
    names = tables(db, database)
    if names != p["tables"]:
        problems.append("表集合不同")
    if db.json(database, SCHEMA_SQL) != db.json(DATABASE if database != DATABASE else p["reference"], SCHEMA_SQL):
        problems.append("结构（列、约束、索引、触发器、函数、授权）与线上不一致")
    ledger = db.json(database, direct.LEDGER_SQL)
    if ledger != p.get("ledger", db.json(DATABASE, direct.LEDGER_SQL) if database != DATABASE else ledger):
        problems.append("迁移 ledger 不一致")
    if epoch(db, database) != p["trusted"]:
        problems.append("身份纪元不一致")
    try:
        # The approved minimal privilege set, measured on this database (other
        # databases: only the live and this one may grant CONNECT).
        direct.check_privileges(db.json(database, direct.privileges_sql(database)))
    except ValueError as e:
        problems.append(str(e))
    c = counts(db, database, names)
    for t in EMPTY:
        if t in c and c[t]:
            problems.append(f"{t} 不为空")
    for t in KEEP_ALL:
        if t in c and c[t] != p["live_counts"][t]:
            problems.append(f"{t} 行数与线上不同")
    if c["teams"] != 1:
        problems.append("团队数不是 1")
    if c["app_users"] != len(p["kept"]) + len(wl["create"]):
        problems.append("人员数与白名单不同")
    members = {m["staff_id"]: m for m in db.json(database, MEMBER_SQL)}
    for k in p["kept"]:
        m = members.get(k["staff_id"])
        same = ("subject", "role", "team_id", "status", "active", "account_state", "phone", "name", "links")
        if not m or any(m[f] != k[f] for f in same):
            problems.append("保留成员身份发生变化：" + k["staff_id"])
    pending = db.json(database, "SELECT coalesce(json_agg(claim_login ORDER BY claim_login),'[]') FROM app_users "
                                "WHERE claim_state='PENDING' AND staff_status='PENDING_PROVISION' AND account_state='NONE' AND work_phone IS NULL")
    if pending != sorted(c2["claim_login"] for c2 in wl["create"]):
        problems.append("待认领成员与白名单不一致")
    admins = int(db.run(database, "SELECT count(*) FROM app_users WHERE role='baobei_admin' AND active"))
    if admins < 1:
        problems.append("没有在用管理员")
    if problems:
        raise Refused("干净库校验失败：" + "；".join(problems))
    return {"counts": c, "ledger_rows": len(ledger), "ledger_sha256": digest(ledger), "issuer": p["trusted"]["issuer"],
            "epoch": p["trusted"]["epoch"], "privileges": "minimal", "schema": "identical",
            "kept": len(p["kept"]), "pending_claims": len(wl["create"])}


def _patch_direct(db):
    """direct-deploy helpers that read through database_query use this psql."""
    def query(docker, container, sql, database=DATABASE, read_only=True):
        return db.run(database, sql, write=not read_only)
    direct.database_query = query


def connections(db, database):
    return int(db.run("postgres", f"SELECT count(*) FROM pg_stat_activity WHERE datname='{database}' AND pid<>pg_backend_pid()"))


def api_stopped(db):
    if db.container is None:
        return
    r = subprocess.run(["docker", "ps", "-q", "--filter", "label=com.docker.compose.project=u-dao-baobei",
                        "--filter", "label=com.docker.compose.service=baobei-api"], capture_output=True, check=True)
    if r.stdout.strip():
        raise Refused("baobei-api 仍在运行：先停止 API（docker compose stop baobei-api）")


def swap(db, state, wl):
    st = read_state(state)
    b = st.get("build")
    if not b or b.get("whitelist_sha256") != wl["sha256"]:
        raise Refused("没有与本白名单对应的已验证干净库：先执行 build")
    if st.get("swap"):
        raise Refused("已执行过切换")
    api_stopped(db)
    for database in (DATABASE, b["database"]):
        if connections(db, database):
            raise Refused(f"{database} 仍有连接：先停止 API 与其他会话")
    require_backup(db, state)
    p = plan(db, wl)
    p["ledger"] = db.json(DATABASE, direct.LEDGER_SQL)
    verify_clean(db, b["database"], p, wl)
    kept = "baobei_pregolive_" + now_tag()
    roles = ",".join(direct.APP_ROLES)
    st["swap"] = {"kept": kept, "clean": b["database"], "at": datetime.now(timezone.utc).isoformat()}
    write_state(state, st)
    db.run("postgres", f"BEGIN; ALTER DATABASE {DATABASE} RENAME TO {kept}; ALTER DATABASE {b['database']} RENAME TO {DATABASE}; "
                       f"REVOKE ALL ON DATABASE {kept} FROM PUBLIC, {roles}; COMMIT;", write=True)
    return {"SWAP": "PASS", "previous_database_kept_as": kept}


def verify_live(db, state, wl):
    st = read_state(state)
    s, b = st.get("swap"), st.get("build")
    if not s or not b:
        raise Refused("尚未切换")
    p = {"tables": b["measured"]["counts"] and sorted(b["measured"]["counts"]), "live_counts": b["measured"]["counts"],
         "trusted": {"issuer": b["measured"]["issuer"], "epoch": b["measured"]["epoch"]},
         "kept": [], "reference": s["kept"]}
    # Kept members are compared with the kept (old) database.
    old = {m["staff_id"]: m for m in db.json(s["kept"], MEMBER_SQL)}
    p["kept"] = [old[k["staff_id"]] for k in wl["keep"]]
    p["ledger"] = db.json(s["kept"], direct.LEDGER_SQL)
    measured = verify_clean(db, DATABASE, p, wl)
    return {"VERIFY": "PASS", **measured}


def rollback(db, state):
    st = read_state(state)
    s = st.get("swap")
    if not s:
        raise Refused("尚未切换，无需回退")
    api_stopped(db)
    for database in (DATABASE, s["kept"]):
        if connections(db, database):
            raise Refused(f"{database} 仍有连接：先停止 API 与其他会话")
    failed = "baobei_golive_rolledback_" + now_tag()
    roles = ",".join(direct.APP_ROLES)
    db.run("postgres", f"BEGIN; ALTER DATABASE {DATABASE} RENAME TO {failed}; ALTER DATABASE {s['kept']} RENAME TO {DATABASE}; "
                       f"GRANT CONNECT ON DATABASE {DATABASE} TO {roles}; REVOKE ALL ON DATABASE {failed} FROM PUBLIC, {roles}; COMMIT;",
           write=True)
    st["rolled_back"] = {"at": datetime.now(timezone.utc).isoformat(), "clean_kept_as": failed}
    st.pop("swap")
    st.pop("build", None)
    write_state(state, st)
    return {"ROLLBACK": "PASS", "restored": DATABASE, "clean_database_kept_as": failed}


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("step", choices=("inventory", "backup", "build", "swap", "verify", "rollback"))
    ap.add_argument("--state-dir", required=True, type=Path)
    ap.add_argument("--whitelist", required=True, type=Path)
    ap.add_argument("--container", help="u-dao-baobei 的 baobei-postgres 容器名")
    ap.add_argument("--local", help="仅隔离演练：127.0.0.1:端口")
    ap.add_argument("--approval", help="写操作必须与白名单 approval 一致")
    ap.add_argument("--operator", default="")
    args = ap.parse_args()
    try:
        state = private_dir(args.state_dir.absolute())
        wl = load_whitelist(args.whitelist.absolute())
        if bool(args.container) == bool(args.local):
            raise Refused("必须且只能指定 --container 或 --local")
        local = None
        if args.local:
            host, _, port = args.local.partition(":")
            local = (host, int(port))
        db = Psql(args.container, local)
        _patch_direct(db)
        if args.step != "inventory":
            if args.approval != wl["approval"]:
                raise Refused("--approval 必须与白名单中的批准编号一致")
            if not re.fullmatch(r"[A-Za-z0-9._-]{1,64}", args.operator or ""):
                raise Refused("需要 --operator（执行人标识）")
        if args.step == "inventory":
            out = {"INVENTORY": "PASS", "DRY_RUN": True, **show_plan(plan(db, wl), wl)}
        elif args.step == "backup":
            out = backup(db, state, wl)
        elif args.step == "build":
            out = build(db, state, wl, args.operator)
        elif args.step == "swap":
            out = swap(db, state, wl)
        elif args.step == "verify":
            out = verify_live(db, state, wl)
        else:
            out = rollback(db, state)
    except Refused as e:
        print(json.dumps({"REFUSED": str(e)}, ensure_ascii=False))
        sys.exit(1)
    print(json.dumps(out, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
