# Phase1-A1.1-O1 证书自动续期与故障回滚

本轮只交付 O1-1 代码、隔离测试、模板和文档，不是 A2。按负责人提供的上线事实：正式 SHA 为7565ee454953af1cbface7e15e23676bdb2b83dd，当前 Crane 使用 TrustAsia DV TLS RSA CA 2024，证书到期日2027-01-27。本轮没有连接生产执行安装、签发、迁移、reload 或 timer enable；真实上线状态和既有证书不被本轮代码交付改变。

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

`bash deploy/scripts/provision-acme.sh --verify-only` 只在临时目录验证上游，不安装、不签发。正式 provisioning 需负责人后续批准，由 root 在审核过的 clean checkout 执行 `bash deploy/scripts/provision-acme.sh --install`；它创建 ACME_HOME/webroot/lock、安装项目脚本与 systemd unit、daemon-reload，但不触碰生产证书、不 reload ingress、不 enable timer。发现已有 active/enabled timer 时拒绝覆盖，要求管理员先明确暂停。

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

路径环境变量供受控打包和临时隔离测试使用，正式运维保持默认；禁止指向符号链接、共享可写目录或其他系统TLS路径。ACME_HOME和STAGE为root:root 0700，marker/lock 0600。共享入口应绑定TLS目录，而不是把单个证书文件inode固定成file bind mount；逐文件rename才能让ingress看到新文件。

后续经批准由root调用 /usr/local/sbin/u-dao-crane-cert-renew：

- --bootstrap：公网probe → issue（LE、单域、webroot、RSA2048）→ 官方 install-cert 仅写 STAGE，明确清空reloadcmd。校验完整PEM、唯一SAN、证书/私钥公钥一致、RSA2048、LE issuer、系统信任链和至少24小时余量。成功只输出 CERT_BOOTSTRAP=READY_FOR_ADOPTION，不切生产、不写adopt marker、不启用timer。
- --adopt：人工明确批准 TrustAsia→LE 的首次迁移后执行。完整candidate通过才创建.production-adopted（0600），再走 guarded promotion；任何失败都撤销本次新marker。fingerprint相同只验证健康并NOOP，不reload。
- --run：timer入口。没有marker则NOOP，连CA也不请求。检查单域state、stage安装路径、固定LE endpoint、无reload/deploy/pre/post hooks，再执行 acme.sh --cron --home ...，让上游ARI/renewal logic决定窗口，不传force。没有新candidate时NOOP、不reload；新candidate仍完整验证后才晋升。
- --check：只读检查生产certificate/key、SAN、有效期、远端HTTPS、严格OIDC issuer和Firebot。不会调用ACME、替换证书或reload。仅使用临时检查文件及现有锁，退出后清理。

ACME的--cron是客户端续期动作，不代表安装操作系统cron。配置只容许一个domain cert目录，并拒绝外部安装目的地和非空hooks，防止客户端自动install绕过stage/guard。root私有状态不可从其他acme实例直接复制，异常时拒绝并人工诊断，不能关闭策略校验。

## Guarded promotion 与失败回滚

持有flock期间校验candidate，读取candidate/current fingerprint。旧生产证书临近到期不能阻止有效新candidate替换；旧pair必须可读且匹配，--check仍拒绝已过期证书。fingerprint相同NOOP，无reload。

不同则先 nginx -t。当前配置失败立即拒绝，不改生产。通过后在ACME_HOME/deploy下创建root-only临时rollback目录，复制当前fullchain/key，文件0600；用同目录临时文件+rename逐文件安装新pair，fullchain0644、privkey0600。再次nginx -t通过后只执行nginx -s reload。不得restart/down/recreate共享入口。

Reload后自动验证：Crane首页、OIDC discovery且issuer精确为 https://crane.youdaozd.com/realms/crane、带hostname/chain验证的远端TLS且fingerprint等于新生产证书、Firebot首页与/health/live。curl不使用insecure选项，OpenSSL s_client必须verify_hostname、verify_return_error及SNI；全部有timeout和有限次数重试。

任一失败恢复旧pair，再nginx -t/reload并重新验证旧服务，输出ROLLED_BACK并非零退出。Rollback本身失败输出CRITICAL、非零退出并停止，写入root-only .renew-critical锁存标记；后续timer拒绝ACME/晋升，防止无人处理的事故被下一次调度继续操作。只有此事故分支保留root-only临时恢复副本供立即人工救援；它不是长期key archive，恢复服务并通过--check后，由负责人批准移除critical标记及临时旧key。正常成功/回滚成功均清除旧private key临时副本，不能上传Git/artifact或做长期归档。

## systemd、暂停与恢复

service为root oneshot、UMask0077，ProtectSystem=strict、独立tmp、ProtectHome、内核与权限加固；只开放ACME/webroot/指定TLS路径写入，不禁用network/OpenSSL/curl/Docker Unix socket。timer每六小时一次、RandomizedDelaySec=30m、Persistent=true，适应ARI与短证书周期。

Bootstrap READY仍不得enable。首次人工adopt成功并复核 --check 后，才由负责人批准执行：

```sh
systemctl enable --now u-dao-crane-cert-renew.timer
systemctl list-timers u-dao-crane-cert-renew.timer
```

暂停：`systemctl disable --now u-dao-crane-cert-renew.timer`。这不撤销当前证书，也不删除私有ACME账户。若service仍在执行，先观察结束，不能在晋升一半时杀进程或改证书文件。维护完成后先--check，确认adoption和私有状态完好，再经批准恢复timer。

首次adopt失败会自动恢复当前TrustAsia pair。成功迁移后不会长期保存旧TrustAsia private key；不能假装能够恢复已删除的旧key。若之后需恢复TrustAsia/另一份current certificate，应暂停timer，从负责人批准的受限证书管理源获得完整有效pair（或另行向原CA申请），人工校验SAN/key/expiry和chain，保存仅限本次操作的root-only rollback副本，按同样的precheck→逐文件安装→nginx -t→reload→--check执行。失败恢复操作前pair并验证；事故处理完成即清理临时旧key。不要用--adopt绕过固定LE候选验证，也不要把恢复材料存放仓库。

日志只允许时间/domain、issuer、notAfter、公开certificate fingerprint、状态NOOP/PROMOTED/ROLLED_BACK/FAIL/CRITICAL等。上游ACME/stdout/stderr不进入journal，不输出PEM、private key、ACME账户密钥、cookie、应用secret、密码或token。故障排查先看脱敏结果、证书公开metadata与--check，不启用shell trace或把上游debug日志上传。

## 隔离验证及后续范围

tests/ops/test_cert_renew.py 在Linux root临时目录生成匿名CA/短期PEM，使用fake ACME/docker/curl与远端s_client、真实本地OpenSSL和flock；无真实CA请求、无生产请求、无持久真实凭据。覆盖NOOP、probe失败/错误body/私网IP、SAN/key/过期/过短、未adopt、nginx前后失败、reload、Crane/OIDC/Firebot/remote TLS失败、rollback CRITICAL、正常晋升、并发、日志脱敏和配置hooks/目的地拒绝。普通Windows下跳过不计通过；CI a1-security以sudo隔离fixture强制执行，并实际运行上游verify-only。HTTP-01/固定依赖/systemd契约纳入repository-contract，仍保持八个必需job，不新增optional job。production-release还把正式HTTP模板渲染进隔离ingress，验证真实nginx中的challenge成功/404与五类普通HTTP路径HTTPS跳转，继续运行既有HTTPS/BFF/Keycloak/Firebot门禁（Firebot外部健康调用在证书脚本fake反证中验证，不在CI联系真实Firebot）。

未来仅在wildcard、port80不可用或ingress架构改变时重新评审dns_tencent。必须独立CAM子用户，限制目标DNS zone与必要DNSPod动作，禁止腾讯云主账号SecretKey。本轮不启用DNS API，也不要求Tencent SecretId/SecretKey。

O1-2（退休bootstrap身份）、O1-3（COS异地备份）、O1-4（HTTPS/OIDC/证书到期/备份年龄/容器健康监控）、O1-5（临时交付文件清理）仅为后续规划，每阶段独立commit和门禁；本轮不实现、不执行，不进入A2。完成O1-1分支交付后等待负责人代码、安全、运维审查。
