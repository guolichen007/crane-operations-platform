# Phase1-A1.1-O1-1-R1 证书持久事务与故障恢复

本轮以 f0ed114bd24947f94dce2c44fa803020be27d5e2 为负责人审查基线，修复 O1-1 的证书晋升中断恢复，只交付代码、隔离测试、模板和文档。按负责人提供的上线事实：正式 SHA 为7565ee454953af1cbface7e15e23676bdb2b83dd，当前 Crane 使用 TrustAsia DV TLS RSA CA 2024，证书到期日2027-01-27。本轮没有连接生产执行安装、签发、迁移、reload 或 timer enable；不修改 main、两个 baseline tag，也不进入 A2。

## 固定依赖与职责

唯一上游为 [acmesh-official/acme.sh 3.1.6](https://github.com/acmesh-official/acme.sh/releases/tag/3.1.6)。官方从该版本开始使用 SSH signed tag；[验证说明](https://github.com/acmesh-official/acme.sh/tree/3.1.6#-verify-a-release)和 allowed_signers 来自同一官方仓库。

冻结值（Git blob LF字节，不接受Windows checkout的CRLF转换）：

| 项目 | 固定值 |
|---|---|
| tag | 3.1.6 |
| annotated tag object | c6e7807f8e2573bf8b02b71da186b4022afafa56 |
| 完整 commit | 807da6498377ee5e0cf43a78091f46f12dc59a89 |
| SSH signer fingerprint | SHA256:M60qVafm/NUywQHXAkoQcj2v6KgkfrdSXv6mPejUUeE |
| allowed_signers SHA256 | 8522e614de31e4e36d1c11e45d90721013b37f91bc3d9aa437fe1844d83400fa |
| acme.sh SHA256 | c7d68b021cfd6380ea83a82962abde5b484779fee0b97d38681dfa1396bbc8d7 |

provision-acme.sh 先 clone 官方HTTPS仓库，提取并核对 allowed_signers，再 git verify-tag 3.1.6，核对 tag/commit/脚本字节。只有全部成功后，才把验证过的 archive 准备到 /opt/u-dao/vendor/acme.sh/3.1.6/。固定文件只读，主脚本0555；保留上游 LICENSE.md，不选择或改变本项目根许可证。该工具是外部运维依赖，不进入 Cloud/Web 镜像。

不使用 curl pipe shell、不调用上游在线安装器、不设置 crontab。运行时 ACME_PACKAGED=1、AUTO_UPGRADE=0；即便配置漂移，上游 packaged 模式也不执行自动升级。升级必须重新提交 PR/CI，复核签名信任锚、版本、完整 SHA 和两份哈希，不能现场 git pull 或运行 upgrade。

`bash deploy/scripts/provision-acme.sh --verify-only` 只在临时目录验证上游，不安装、不签发。正式 provisioning 需负责人后续批准，由 root 在审核过的 clean checkout 执行 `bash deploy/scripts/provision-acme.sh --install`；它创建 ACME_HOME/webroot/lock、安装项目脚本与 systemd unit、daemon-reload，但不触碰生产证书、不 reload ingress、不 enable timer。安装前必须确认 service inactive（首次未安装允许 unknown）、timer inactive/disabled；已有 renewal lock 时必须先 nonblocking 获取同一个锁，并持有至安装结束。service 正在执行或锁被占用时，在下载或覆盖脚本/unit 前拒绝。首次安装以排他创建锁文件，后续不替换锁 inode。

项目入口安装为 `/usr/local/sbin/u-dao-crane-cert-renew`，持久状态助手安装为 `/usr/local/libexec/u-dao-crane-cert-state`，均为 root 所有、0755。助手使用 Linux fsync 与同目录 rename，不依赖 shell EXIT trap 实现掉电恢复。

依赖：Linux Bash、util-linux flock、coreutils/timeout、OpenSSL、curl、Python3、Git、支持SSH签名的ssh-keygen、Docker CLI、systemd。升级和脚本部署需维持root所有权；不能把可被普通用户写入的脚本当root服务执行。

## HTTP-01 前置条件

仅签 crane.youdaozd.com，RSA 2048，显式 --server letsencrypt。HTTP-01 webroot 为 /srv/u-dao/platform/ingress/acme-webroot；shared ingress 必须把该目录只读挂到 /var/www/acme。

模板为 deploy/ingress/crane-http01.conf.example 与 crane-acme-webroot.compose.example.yml。后者仅供审核后合入既有 shared ingress Compose，不能作为另一套 stack 启动，也不能覆盖 Firebot 现有 volume/network。若现有容器缺少该挂载，停止 bootstrap，交负责人安排共享入口挂载变更；provision/renew 不会 restart、down 或 recreate ingress。

Crane HTTP server 的 challenge location 使用 ^~、root、try_files、access_log off、no-store；redirect 放在 location /，不能保留 server级 blanket return 抢先处理challenge。只审核 Crane server block，不能修改 Firebot server block。Crane HTTPS /realms、/resources、/api、/auth、/ 路由保持原模板。

Bootstrap会写入一次性probe文件，使用固定公网HTTP域名、禁用代理、拒绝非公网remote IP、不跟随HTTP重定向，逐字比对响应。probe失败不会联系CA。只有隔离测试使用fake curl；正式流程不得以localhost、自签名TLS或内部DNS模拟结果冒充公网probe。

## 路径与四个模式

| 名称 | 默认值 |
|---|---|
| DOMAIN | crane.youdaozd.com（固定，不允许扩域） |
| ACME_HOME | /srv/u-dao/secrets/crane-ops/acme |
| WEBROOT | /srv/u-dao/platform/ingress/acme-webroot |
| STAGE | $ACME_HOME/deploy/crane.youdaozd.com |
| PRODUCTION_TLS | /srv/u-dao/platform/ingress/tls/crane |
| INGRESS | u-dao-ingress-nginx-1（固定） |
| 生产文件 | fullchain.pem / privkey.pem |

路径环境变量供受控打包和临时隔离测试使用，正式运维保持默认；禁止指向符号链接、共享可写目录或其他系统TLS路径。ACME_HOME、WEBROOT和PRODUCTION_TLS三根目录必须互不重合、互不嵌套，避免bootstrap/stage写入生产或把私有状态混入webroot；错误配置在CA请求前拒绝。ACME_HOME、deploy、STAGE及rollback目录为root:root 0700，journal/marker/lock/私有副本为0600。共享入口应绑定TLS目录，而不是把单个证书文件inode固定成file bind mount；逐文件rename才能让ingress看到新文件。

后续经批准由root调用 /usr/local/sbin/u-dao-crane-cert-renew：

- --bootstrap：公网probe → issue（LE、单域、webroot、RSA2048）→ 官方 install-cert 仅写 STAGE，明确清空reloadcmd。校验完整PEM、唯一SAN、证书/私钥公钥一致、RSA2048、LE issuer、系统信任链和至少604800秒（7天）剩余有效期。成功只输出 CERT_BOOTSTRAP=READY_FOR_ADOPTION，不切生产、不写adopt marker、不启用timer。
- --adopt：人工明确批准 TrustAsia→LE 的首次迁移后执行。先检查 pending，再校验candidate、guarded promotion及完整post-health；全部成功才原子创建.production-adopted（0600）。若上次晋升已完成但marker未写，重跑时通过相同fingerprint和完整health安全补齐marker，输出NOOP，不reload。
- --run：timer入口。优先恢复pending；没有pending且没有marker时NOOP，连CA也不请求。检查单域state、stage安装路径、固定LE endpoint、无reload/deploy/pre/post hooks，再执行 acme.sh --cron --home ...，让上游ARI/renewal logic决定窗口，不传force。已adopt且candidate相同也必须验证production pair及全部健康门禁，成功才NOOP，不reload；新candidate仍完整验证后才晋升。
- --check：只读检查生产certificate/key、SAN、有效期、远端HTTPS、严格OIDC issuer和Firebot。不会调用ACME、替换证书或reload。仅使用临时检查文件及现有锁，退出后清理。

ACME的--cron是客户端续期动作，不代表安装操作系统cron。配置只容许一个domain cert目录，并拒绝外部安装目的地和非空hooks，防止客户端自动install绕过stage/guard。root私有状态不可从其他acme实例直接复制，异常时拒绝并人工诊断，不能关闭策略校验。

## 持久事务、Guarded promotion 与恢复

所有变更模式持有同一flock，先检查 `$ACME_HOME/.promotion-pending`，在 CA、candidate 校验和 adoption/critical 常规分支之前处理未完成事务。`--check` 不改变 journal 或证书。不存在pending/critical时才执行受限GC，再继续常规流程。

持有flock期间校验candidate，读取candidate/current fingerprint。旧生产证书临近到期不能阻止有效新candidate替换；旧pair必须可读且匹配，--check仍拒绝已过期证书。相同fingerprint时也执行下面的完整health，成功才NOOP。

不同则先 nginx -t。当前配置失败立即拒绝，不改生产。通过后在ACME_HOME/deploy下创建root-only `.rollback.*`，复制当前fullchain/key，文件0600并fsync文件和目录。随后持久写入 `.promotion-pending`：schema=1、严格rollback basename、old/candidate SHA256 fingerprint、原模式和原adopt状态。journal通过同目录临时文件、fchmod0600、写入/fsync、atomic rename、父目录fsync完成；第一次修改production TLS之前必须完成该步骤。

恢复路径只接受规定格式的 `.rollback.*` basename，逐项验证canonical路径严格位于deploy、root:root目录0700、两份普通文件0600、无symlink、无额外文件；不按journal提供的任意路径读取。old fingerprint必须匹配副本。格式错误、越界、缺失副本或不安全权限均进入CRITICAL，不联系CA。

安装时先准备并fsync同一production目录中的临时pair，再逐文件rename与父目录fsync，fullchain0644、privkey0600。两文件不是一个原子操作；持久journal负责判定和恢复中间状态。再次nginx -t通过后只执行nginx -s reload。不得restart/down/recreate共享入口。

Reload后自动验证：Crane首页、OIDC discovery且issuer精确为 https://crane.youdaozd.com/realms/crane、带hostname/chain验证的远端TLS且fingerprint等于新生产证书、Firebot首页与/health/live。curl不使用insecure选项，OpenSSL s_client必须verify_hostname、verify_return_error及SNI；全部有timeout和有限次数重试。

正常调用发生失败时恢复旧pair，再nginx -t/reload并重新验证旧服务，输出ROLLED_BACK并非零退出。SIGKILL/OOM/掉电不执行trap；下次 `--adopt` 或 `--run` 发现pending时，一律从受校验副本恢复旧pair，再nginx -t/reload及完整Crane/OIDC/远端fingerprint/Firebot健康检查。成功后清除pending与副本，输出RECOVERED_ROLLBACK并以3退出，本轮停止，不继续续期。

| 中断时的持久状态 | 下次变更调用 |
|---|---|
| cert新/key旧 | 忽略坏pair，从journal恢复旧pair |
| pair均新、nginx尚未reload | 恢复旧pair并reload/health |
| 已reload、health未完成 | 恢复旧pair并reload/health |
| health已通过、pending尚未清除 | 仍保守恢复旧pair |
| pending已清除、adopt marker未写 | 重跑adopt，完整health后补齐marker |

Rollback或启动recovery失败则输出CRITICAL、非零停止，持久写入root-only `.renew-critical`，保留pending和rollback副本。pending恢复优先于critical常规分支；即使恢复成功，既有critical仍不自动清除，后续run拒绝CA。首次adopt恢复到原pair后不保留adopt授权。人工救援必须暂停调度、检查journal/副本和服务状态；恢复并通过--check后，由负责人批准清除critical，不得直接删除pending绕过恢复。

正常晋升、正常回滚或启动恢复通过health后，先持久清除pending，再删除private rollback副本并fsync deploy。若恰在两者之间中断，下次在锁内确认没有pending/critical，验证孤立 `.rollback.*` 的归属、权限、canonical目录和文件白名单后清理，同时清理仅属于该事务的production临时文件（包括rename前中断的临时私钥）。发现不安全副本拒绝并锁存critical，不能盲删。critical存在时不做孤儿清理。恢复材料不是长期key archive，不可进入Git/artifact。

## systemd、暂停与恢复

service为root oneshot、UMask0077，ProtectSystem=strict、独立tmp、ProtectHome、内核与权限加固；只开放ACME/webroot/指定TLS路径写入，不禁用network/OpenSSL/curl/Docker Unix socket。timer每六小时一次、RandomizedDelaySec=30m、Persistent=true，适应ARI与短证书周期。

生产启用 Gate：本轮timer始终disabled。Bootstrap READY、首次人工adopt成功和--check通过，均不构成timer启用许可。必须等O1-4的HTTPS/OIDC/证书到期/备份年龄/容器健康监控与告警完成并验收，再由负责人另行批准启用。O1-4完成前不得正式enable；本轮不提供立即启用步骤。

暂停：`systemctl disable --now u-dao-crane-cert-renew.timer`。这不撤销当前证书，也不删除私有ACME账户。若service仍在执行，先观察结束，不能在晋升一半时杀进程或改证书文件。若已意外终止，按pending恢复流程处理。维护后先--check，确认adoption和私有状态完好；仍须满足O1-4 Gate并获批准后恢复timer。

首次adopt失败会自动恢复当前TrustAsia pair。成功迁移后不会长期保存旧TrustAsia private key；不能假装能够恢复已删除的旧key。若之后需恢复TrustAsia/另一份current certificate，应暂停timer，从负责人批准的受限证书管理源获得完整有效pair（或另行向原CA申请），人工校验SAN/key/expiry和chain，保存仅限本次操作的root-only rollback副本，按同样的precheck→逐文件安装→nginx -t→reload→--check执行。失败恢复操作前pair并验证；事故处理完成即清理临时旧key。不要用--adopt绕过固定LE候选验证，也不要把恢复材料存放仓库。

日志只允许时间/domain、issuer、notAfter、公开certificate fingerprint、状态NOOP/PROMOTED/ROLLED_BACK/RECOVERED_ROLLBACK/FAIL/CRITICAL及脱敏错误码。上游ACME stdout/stderr只在内存中保留末尾最多64KiB用于分类，成功或失败均不写journal/磁盘。`$ACME_HOME/cert-diagnostics.jsonl` 仅存时间、分类、退出码、操作：network、CA、challenge、state-policy、validation；root:root 0600，单份最大32KiB，轮换最多3份。分类基于上游错误特征，不能代替管理员复核原因。不输出PEM、private key、ACME账户密钥、cookie、应用secret、密码或token；diagnostic不进入validation/artifact/Git。故障排查先看脱敏结果、公开metadata与--check，不启用shell trace或上传上游debug日志。

## 隔离验证及后续范围

tests/ops/test_cert_renew.py 在Linux root临时目录生成匿名CA/短期PEM，使用fake ACME/docker/curl与远端s_client、真实本地OpenSSL和flock；无真实CA请求、无生产请求、无持久真实凭据。保留原有成功/回滚/策略/并发门禁，增加四类pending中间态、真实SIGKILL后混合pair恢复、marker失败后补齐、NOOP健康失败、malformed/越界/缺失/符号链接/权限错误journal、孤儿清理与critical保留、6天拒绝/8天接受、安装service/锁拒绝及有界脱敏diagnostic。普通Windows下跳过不计通过。

CI a1-security 必须先对两个入口执行 `bash -n`，再通过 tests/ops/verify-cert-units.sh 调用真实 `systemd-analyze verify` 校验service和timer；仅在私有临时副本中重定位ExecStart至同样脚本的隔离路径，并提供dependency stub，原始绝对ExecStart由repository-contract覆盖。不daemon-reload/start/enable。随后实际运行上游verify-only及sudo隔离运维fixture，不新增optional job，仍完整重跑八个必需job。production-release继续验证真实nginx HTTP-01、既有HTTPS/BFF/真实Keycloak/备份恢复门禁（Firebot外部健康调用在证书脚本fake反证中验证，不在CI联系真实Firebot）。

未来仅在wildcard、port80不可用或ingress架构改变时重新评审dns_tencent。必须独立CAM子用户，限制目标DNS zone与必要DNSPod动作，禁止腾讯云主账号SecretKey。本轮不启用DNS API，也不要求Tencent SecretId/SecretKey。

O1-2（退休bootstrap身份）、O1-3（COS异地备份）、O1-4（HTTPS/OIDC/证书到期/备份年龄/容器健康监控与告警）、O1-5（临时交付文件清理）仅为后续规划，每阶段独立commit和门禁；本轮不实现、不执行，不进入A2。完成O1-1-R1分支交付后停止，等待负责人R1审查。
