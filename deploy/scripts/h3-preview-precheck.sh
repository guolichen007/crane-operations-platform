#!/usr/bin/env bash
# H3-P G1 —— Preview 服务器只读预检。
# 状态（D1，2026-11-10）：OWNER 已取消独立 b4 Preview；本脚本仅为既有 b4 基座的只读盘点保留，
# 不再是开发发布流程的一环（开发发布见 deploy/scripts/dev-upgrade.mjs）。
#
# 严格只读：本脚本不执行 docker compose up/down/rm/restart、不写 Nginx/证书/网络/卷/数据库、不连接任何数据库、
# 不落盘任何文件。密钥只用 cmp 判断两套是否相同，输出仅 SAME / DIFFERENT / NOT_ACCESSIBLE，
# 不输出任何哈希或其片段。resolved compose 只经管道交给契约检查，不打印、不保存。
# 运行方式（由 OWNER / 服务器 CC 在 Preview 服务器上执行，普通用户 + 需要时 sudo 读 docker）：
#
#   bash deploy/scripts/h3-preview-precheck.sh > h3-precheck-$(date +%Y%m%dT%H%M%S).txt 2>&1
#
# 可选环境变量（只影响读取目标，不改变行为）：
#   PREVIEW_HOST       默认 b4.crane.youdaozd.com
#   PROD_HOST          默认 crane.youdaozd.com
#   PREVIEW_PROJECT    默认 crane-b4-preview
#   PROD_PROJECT       默认 u-dao-crane-ops
#   PREVIEW_ENV_FILE   若提供且仓库存在，则额外执行只读 `docker compose config`（不启动任何容器）
#
# 输出末尾给出 PRECHECK_SUMMARY 行，供 CC 生成 G1 结论；结论本身由人审，不由脚本自动批准部署。
set -u
export LC_ALL=C

PREVIEW_HOST="${PREVIEW_HOST:-b4.crane.youdaozd.com}"
PROD_HOST="${PROD_HOST:-crane.youdaozd.com}"
PREVIEW_PROJECT="${PREVIEW_PROJECT:-crane-b4-preview}"
PROD_PROJECT="${PROD_PROJECT:-u-dao-crane-ops}"
PREVIEW_ENV_FILE="${PREVIEW_ENV_FILE:-}"
PREVIEW_NETS=(u-dao-crane-b4-preview-frontend u-dao-crane-b4-preview-api-frontend)
PROD_NETS=(u-dao-crane-frontend)
PROD_VOLUME_HINTS='crane-postgres|crane-mqtt|/srv/u-dao/secrets/crane-ops'

FINDINGS=()
note() { FINDINGS+=("$1"); }
section() { printf '\n===== %s =====\n' "$1"; }
have() { command -v "$1" >/dev/null 2>&1; }
# 统一的密钥形态脱敏：任何像 token/password/secret=... 的值都替换掉。
redact() { sed -E 's/((password|passwd|secret|token|key)[^=:"]*["]?[=:][ "]*)[^ ",}]+/\1<REDACTED>/Ig'; }

DOCKER=docker
if have docker && ! docker info >/dev/null 2>&1; then
  if have sudo && sudo -n docker info >/dev/null 2>&1; then DOCKER="sudo -n docker"; fi
fi

section "0. 元信息"
echo "precheck_script=h3-preview-precheck.sh mode=READ_ONLY"
echo "time_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "hostname=$(hostname)"
echo "user=$(id -un) uid=$(id -u)"
echo "preview_host=$PREVIEW_HOST prod_host=$PROD_HOST preview_project=$PREVIEW_PROJECT prod_project=$PROD_PROJECT"

section "1. 操作系统与资源"
[ -r /etc/os-release ] && grep -E '^(PRETTY_NAME|VERSION_ID)=' /etc/os-release
uname -srm
echo "cpus=$(nproc 2>/dev/null)"
free -m 2>/dev/null
df -hT -x tmpfs -x devtmpfs -x overlay 2>/dev/null
uptime
avail_kb=$(df -Pk / 2>/dev/null | awk 'NR==2{print $4}')
[ -n "${avail_kb:-}" ] && [ "$avail_kb" -lt 10485760 ] && note "WARN root 可用空间 < 10GiB（${avail_kb}KB）"
mem_mb=$(free -m 2>/dev/null | awk '/^Mem:/{print $7}')
[ -n "${mem_mb:-}" ] && [ "$mem_mb" -lt 2600 ] && note "WARN 可用内存 ${mem_mb}MiB < Preview 预算约 2.3GiB+余量"

section "2. Docker / Compose"
if ! have docker; then
  note "BLOCK 未找到 docker 命令"
else
  $DOCKER version --format 'server={{.Server.Version}} client={{.Client.Version}} api={{.Server.APIVersion}}' 2>&1
  $DOCKER compose version 2>&1
  $DOCKER info --format 'storage={{.Driver}} root={{.DockerRootDir}} containers={{.Containers}} running={{.ContainersRunning}} images={{.Images}}' 2>&1
  $DOCKER system df 2>&1
  echo "--- compose 项目"
  $DOCKER compose ls --all 2>&1
  echo "--- 全部容器（名称/镜像/状态/端口/compose 项目）"
  $DOCKER ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.Label "com.docker.compose.project"}}' 2>&1
  echo "--- 非 healthy/running 的容器"
  $DOCKER ps -a --filter health=unhealthy --format '{{.Names}} UNHEALTHY' 2>&1
  $DOCKER ps -a --filter status=exited --filter status=restarting --format '{{.Names}} {{.Status}}' 2>&1
fi

section "3. 生产与在用服务健康（只读）"
if have docker; then
  prod_cnt=$($DOCKER ps -q --filter "label=com.docker.compose.project=$PROD_PROJECT" 2>/dev/null | wc -l)
  echo "prod_running_containers=$prod_cnt"
  $DOCKER ps --filter "label=com.docker.compose.project=$PROD_PROJECT" \
    --format '{{.Names}}\t{{.Image}}\t{{.Status}}' 2>&1
  [ "$prod_cnt" -eq 0 ] && note "INFO 未发现运行中的生产项目 $PROD_PROJECT（核对项目名）"
  echo "--- 生产镜像引用（只读 inspect，确认非 latest）"
  for c in $($DOCKER ps -q --filter "label=com.docker.compose.project=$PROD_PROJECT" 2>/dev/null); do
    $DOCKER inspect --format '{{.Name}} image={{.Config.Image}} revision={{index .Config.Labels "org.opencontainers.image.revision"}}' "$c" 2>&1
  done
fi
for u in "https://$PROD_HOST/healthz" "https://$PROD_HOST/" ; do
  have curl && printf '%s -> %s\n' "$u" "$(curl -s -o /dev/null -m 10 -w '%{http_code} t=%{time_total}s' "$u" 2>&1)"
done

section "4. 80/443 入口与 Nginx（只读）"
have ss && ss -ltnp 2>/dev/null | awk 'NR==1 || /:(80|443|18081|18082|18083|18084|18085|5432|1883|8883)\b/'
for p in 18083 18085; do
  if have ss && ss -ltn 2>/dev/null | grep -qE "[: ]$p\b"; then
    bind=$(ss -ltn 2>/dev/null | awk -v p=":$p" '$4 ~ p"$" {print $4}' | tr '\n' ' ')
    echo "port $p in use: $bind"
    echo "$bind" | grep -qvE '^(127\.0\.0\.1|\[::1\]):' && echo "$bind" | grep -qE '(0\.0\.0\.0|\*|\[::\]):' && note "BLOCK 端口 $p 绑定到非回环地址：$bind"
  else
    echo "port $p free"
  fi
done
if have nginx; then
  nginx -v 2>&1
  # nginx -T 只读输出合并配置；只摘取路由相关指令，不输出证书私钥内容（配置里只有路径）。
  { nginx -T 2>/dev/null || sudo -n nginx -T 2>/dev/null; } | grep -nE '^# configuration file|server_name|listen|ssl_certificate|proxy_pass|set_real_ip_from|real_ip_header|X-Forwarded' | redact | head -300
elif have docker; then
  echo "宿主机无 nginx；查找入口代理容器"
  $DOCKER ps --format '{{.Names}}\t{{.Image}}\t{{.Ports}}' 2>/dev/null | grep -Ei 'nginx|caddy|traefik|ingress|haproxy|:443->' || echo "未找到入口代理容器"
fi

section "5. DNS / HTTPS / 证书"
for h in "$PREVIEW_HOST" "$PROD_HOST"; do
  echo "--- $h"
  if have dig; then dig +short A "$h"; dig +short AAAA "$h"; dig +short CNAME "$h"
  elif have getent; then getent ahosts "$h" | awk '{print $1}' | sort -u
  fi
  if have openssl; then
    echo | timeout 15 openssl s_client -servername "$h" -connect "$h:443" 2>/dev/null \
      | openssl x509 -noout -subject -issuer -dates -ext subjectAltName 2>/dev/null || echo "TLS 握手失败或无证书"
  fi
  have curl && printf 'GET https://%s/ -> %s\n' "$h" "$(curl -s -o /dev/null -m 10 -w '%{http_code}' "https://$h/" 2>&1)"
done
if have dig && [ -z "$(dig +short "$PREVIEW_HOST")" ]; then note "BLOCK $PREVIEW_HOST 无 DNS 记录"; fi
echo "--- 证书目录（仅列名与修改时间，不读内容）"
for d in /etc/letsencrypt/live /srv/u-dao/certs /srv/u-dao/acme; do
  [ -d "$d" ] && { echo "$d:"; ls -la "$d" 2>&1 | awk '{print $6,$7,$8,$9}'; }
done

section "6. 既有 Preview 占用情况（$PREVIEW_PROJECT）"
if have docker; then
  pv_cnt=$($DOCKER ps -aq --filter "label=com.docker.compose.project=$PREVIEW_PROJECT" 2>/dev/null | wc -l)
  echo "preview_containers_total=$pv_cnt"
  $DOCKER ps -a --filter "label=com.docker.compose.project=$PREVIEW_PROJECT" \
    --format '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}\t{{.Label "com.docker.compose.config-files"}}' 2>&1
  for c in $($DOCKER ps -aq --filter "label=com.docker.compose.project=$PREVIEW_PROJECT" 2>/dev/null); do
    $DOCKER inspect --format '{{.Name}} image={{.Config.Image}} revision={{index .Config.Labels "org.opencontainers.image.revision"}} created={{.Created}} started={{.State.StartedAt}}' "$c" 2>&1
    # 只输出环境变量名与非敏感哨兵值，不输出任何值中可能含密钥的变量。
    $DOCKER inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$c" 2>/dev/null \
      | grep -E '^(CRANE_DEPLOYMENT_ENVIRONMENT|PUBLIC_URL|OIDC_ISSUER|OIDC_CLIENT_ID|APP_ENV|KC_HOSTNAME)=' | sed "s|^|  $c env |"
  done
  [ "$pv_cnt" -gt 0 ] && note "INFO 已存在 $PREVIEW_PROJECT 容器 $pv_cnt 个：判断是否被其他任务占用前不得覆盖/停止/删除"
  echo "--- 其他占用 18083/18085 的容器"
  $DOCKER ps --format '{{.Names}}\t{{.Ports}}\t{{.Label "com.docker.compose.project"}}' 2>/dev/null | grep -E ':(18083|18085)->' || echo "无"
fi

section "7. 数据卷与项目归属"
if have docker; then
  $DOCKER volume ls --format '{{.Name}}\t{{.Driver}}\t{{.Label "com.docker.compose.project"}}' 2>&1
  for v in $($DOCKER volume ls -q 2>/dev/null | grep -E 'preview|b4|crane' ); do
    $DOCKER volume inspect --format '{{.Name}} project={{index .Labels "com.docker.compose.project"}} created={{.CreatedAt}} mountpoint={{.Mountpoint}}' "$v" 2>&1
  done
  echo "--- Preview 容器挂载（不得出现生产卷/生产密钥目录）"
  for c in $($DOCKER ps -aq --filter "label=com.docker.compose.project=$PREVIEW_PROJECT" 2>/dev/null); do
    m=$($DOCKER inspect --format '{{.Name}} {{range .Mounts}}{{.Type}}:{{.Name}}{{.Source}}->{{.Destination}}(rw={{.RW}}) {{end}}' "$c" 2>&1)
    echo "$m"
    echo "$m" | grep -qE "$PROD_VOLUME_HINTS" && note "BLOCK Preview 容器 $c 挂载了生产卷或生产密钥目录"
  done
  pg=$($DOCKER ps -aq --filter "label=com.docker.compose.project=$PREVIEW_PROJECT" --filter label=com.docker.compose.service=postgres 2>/dev/null | head -1)
  [ -n "$pg" ] && echo "preview_pg_data_volume=$($DOCKER inspect --format '{{range .Mounts}}{{if eq .Destination "/var/lib/postgresql/data"}}{{.Name}}{{end}}{{end}}' "$pg")"
fi

section "8. 网络隔离"
if have docker; then
  $DOCKER network ls --format '{{.Name}}\t{{.Driver}}\t{{.Internal}}\t{{.Label "com.docker.compose.project"}}' 2>&1
  for n in "${PREVIEW_NETS[@]}" "${PROD_NETS[@]}"; do
    if $DOCKER network inspect "$n" >/dev/null 2>&1; then
      $DOCKER network inspect "$n" --format '{{.Name}} internal={{.Internal}} labels={{json .Labels}} subnets={{json .IPAM.Config}}' 2>&1
      $DOCKER network inspect "$n" --format '{{range $id,$c := .Containers}}  member {{$c.Name}} {{$c.IPv4Address}}{{println}}{{end}}' 2>&1
    else
      echo "$n: 不存在"
      case " ${PREVIEW_NETS[*]} " in *" $n "*) note "INFO Preview 网络 $n 不存在（另建实例时需 OWNER 批准创建）";; esac
    fi
  done
  echo "--- 交叉成员（同时在 Preview 与生产网络上的容器 = 隔离破坏）"
  for c in $($DOCKER ps -q 2>/dev/null); do
    nets=$($DOCKER inspect --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}} {{end}}' "$c" 2>/dev/null)
    name=$($DOCKER inspect --format '{{.Name}}' "$c" 2>/dev/null)
    inpv=0; inprod=0
    for n in $nets; do
      case "$n" in u-dao-crane-b4-preview-*|${PREVIEW_PROJECT}_*) inpv=1;; esac
      case "$n" in u-dao-crane-frontend|${PROD_PROJECT}_*) inprod=1;; esac
    done
    if [ $inpv = 1 ] && [ $inprod = 1 ]; then
      echo "CROSS $name: $nets"
      echo "$name" | grep -qiE 'ingress|nginx|proxy|caddy|traefik' \
        && note "INFO 入口代理 $name 同时连接 Preview 与生产前端网络（预期内，需确认仅转发）" \
        || note "BLOCK 非入口容器 $name 同时在 Preview 与生产网络"
    fi
  done
  echo "--- 预留子网 10.240.40.0/24 占用"
  $DOCKER network ls -q 2>/dev/null | xargs -r $DOCKER network inspect --format '{{.Name}} {{range .IPAM.Config}}{{.Subnet}} {{end}}' 2>/dev/null | grep '10\.240\.40\.' || echo "未被占用"
fi

section "9. Keycloak（只读公开端点，不使用管理员凭据）"
for base in "https://$PREVIEW_HOST" "http://127.0.0.1:18085"; do
  for realm in crane-preview crane; do
    u="$base/realms/$realm/.well-known/openid-configuration"
    if have curl; then
      body=$(curl -s -m 10 "$u" 2>/dev/null)
      code=$(curl -s -o /dev/null -m 10 -w '%{http_code}' "$u" 2>/dev/null)
      iss=$(printf '%s' "$body" | sed -nE 's/.*"issuer":"([^"]+)".*/\1/p')
      echo "$u -> $code issuer=${iss:-<none>}"
    fi
  done
done
echo "提示：client crane-b4-preview 的 redirect/webOrigins/安全开关请用 provision-keycloak --preview-precheck（只读 GET）核对；本脚本不读取管理员密码。"
if have docker; then
  kc=$($DOCKER ps -q --filter "label=com.docker.compose.project=$PREVIEW_PROJECT" --filter label=com.docker.compose.service=keycloak 2>/dev/null | head -1)
  [ -n "$kc" ] && $DOCKER inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$kc" | grep -E '^KC_(HOSTNAME|PROXY[A-Z_]*|HTTP_ENABLED|DB_URL)=' | sed 's|^|  keycloak env |'
fi

section "10. MQTT / 会话密钥目录（仅存在性与权限，不读内容）"
for d in /srv/u-dao/secrets/crane-b4-preview /srv/u-dao/secrets/crane-ops; do
  if sudo -n test -d "$d" 2>/dev/null || [ -d "$d" ]; then
    echo "$d: 存在"
    { ls -la "$d" 2>/dev/null || sudo -n ls -la "$d" 2>/dev/null; } | awk '{print $1,$3,$4,$9}'
  else
    echo "$d: 不存在或无权限"
  fi
done
echo "--- 两套密钥是否相同（只输出 SAME / DIFFERENT / NOT_ACCESSIBLE，不输出任何哈希或其片段）"
for f in session_server_secret oidc_client_secret app_db_password; do
  verdict=NOT_ACCESSIBLE
  if sudo -n test -r "/srv/u-dao/secrets/crane-b4-preview/$f" 2>/dev/null && sudo -n test -r "/srv/u-dao/secrets/crane-ops/$f" 2>/dev/null; then
    if sudo -n cmp -s "/srv/u-dao/secrets/crane-b4-preview/$f" "/srv/u-dao/secrets/crane-ops/$f" 2>/dev/null; then verdict=SAME; else verdict=DIFFERENT; fi
  fi
  echo "$f $verdict"
  [ "$verdict" = SAME ] && note "BLOCK Preview 的 $f 与生产相同"
done

section "11. 镜像、分发与回滚条件"
if have docker; then
  $DOCKER images --digests --format '{{.Repository}}:{{.Tag}}\t{{.Digest}}\t{{.CreatedSince}}\t{{.Size}}' 2>/dev/null | grep -Ei 'crane|cloud-api|web-console' | head -40
  $DOCKER images --format '{{.Repository}}:{{.Tag}}' 2>/dev/null | grep -Ei 'crane|cloud-api|web-console' | grep -q ':latest$' && note "INFO 本机存在 :latest 标签的 crane 镜像（不得作为验收依据）"
  echo "ghcr 登录配置存在性：$( [ -f "$HOME/.docker/config.json" ] && grep -q ghcr.io "$HOME/.docker/config.json" && echo yes || echo no )"
fi
echo "--- 备份工具与最近备份（仅列名）"
for d in /srv/u-dao/backups /srv/u-dao/backup /var/backups/crane; do [ -d "$d" ] && { echo "$d:"; ls -lt "$d" 2>/dev/null | head -5; }; done

if [ -n "$PREVIEW_ENV_FILE" ] && [ -f deploy/compose/docker-compose.b4-preview.yml ]; then
  section "12. 只读 compose config（不启动容器，解析结果只经管道、不落盘、不打印）"
  if $DOCKER compose --project-name "$PREVIEW_PROJECT" --env-file "$PREVIEW_ENV_FILE" \
    -f deploy/compose/docker-compose.prod.yml -f deploy/compose/docker-compose.b4-preview.yml \
    config --format json 2>/dev/null | { have node && node tools/b4-preview-contract.mjs --resolved - >/dev/null 2>&1; }; then
    echo "compose config 契约：PASS"
  else
    echo "compose config 契约：FAIL（为避免泄露，不输出解析内容；请在服务器本地人工复核）"
    note "BLOCK resolved compose 契约未通过"
  fi
fi

section "PRECHECK_SUMMARY"
blocks=0
for f in "${FINDINGS[@]+"${FINDINGS[@]}"}"; do echo "$f"; case "$f" in BLOCK*) blocks=$((blocks+1));; esac; done
echo "PRECHECK_BLOCKERS=$blocks"
echo "PRECHECK_MODE=READ_ONLY (no compose up/down, no config/cert/network/db writes)"
