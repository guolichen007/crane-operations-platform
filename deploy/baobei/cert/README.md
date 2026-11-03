# Baobei 独立 HTTP-01 证书事务

代码/测试/模板交付；ACME_SERVER_APPLIED=NO，S1B_PUBLIC_DEPLOY=NOT_AUTHORIZED。本轮不运行 installer、bootstrap/adopt、systemctl、真实证书或服务器操作。源自 Crane O1-1 事务设计的独立副本，Crane 原实现未修改。

固定 DOMAIN=baobei.youdaozd.com；ACME_HOME=/srv/u-dao/secrets/baobei/acme；WEBROOT=/srv/u-dao/platform/ingress/acme-webroot；TLS_DIR=/srv/u-dao/platform/ingress/tls/baobei（脚本内部 PRODUCTION_TLS）。单位 u-dao-baobei-cert-renew.service/.timer。仅共享 HTTP-01 静态 webroot 和 ingress 容器 u-dao-ingress-nginx-1，不读取 Crane/Firebot 证书或探测其域名。

固定 acme.sh 3.1.6，official signed tag + tag object + commit + allowed_signers SHA256 + acme.sh SHA256 全部验证，独立只读 vendor /opt/u-dao/vendor/baobei/acme.sh/3.1.6。无 curl|sh、自动升级或浮动最新版。provision-acme.sh 安装前拒绝运行中的 service、启用的 timer、已持有的 renewal lock；安装只 daemon-reload，不启用、不启动、不签发、不改 TLS。

未来独立授权后：先由负责人配置共享 ingress HTTP-01 webroot/TLS 挂载，准备 root-owned 私有 state/lock 与可信人工初始 live pair；installer 验证上游并安装；--bootstrap 先验证公网 HTTP-01 body/remote IP，再签发及验证到私有 stage，不直接推广；--adopt 在单独批准后推广并记录 adoption；后续 timer 仅对已 adoption 状态执行 --run。--check 只检查当前证书及服务。timer 默认不启用，adoption 和 timer 启用各自需要审批。

人工 TrustAsia bootstrap-fullchain.pem/bootstrap-privkey.pem 是不可覆盖的 bootstrap/emergency fallback。live 文件名仅 fullchain.pem/privkey.pem；初始复制或紧急恢复需负责人另行授权并保持 key 0600，不将 bootstrap-* 作为自动续签目标，不把 CA/key material 放入 Git。回退使用事务前 live pair 的私有备份，不覆盖人工 fallback。

候选要求精确唯一 DNS SAN、私钥公钥匹配、RSA2048、Let's Encrypt issuer、可信链/hostname/有效期验证且至少剩 7 天。过期的旧 live pair 不阻止合法新候选。证书 PEM 中禁止混入私钥。
推广前 nginx -t；先持久化 rollback pair 和 promotion journal，再原子替换 live 文件、fsync、nginx -t/reload、HTTPS 首页/ready JSON 与远端证书指纹/可信链探测。失败恢复旧 pair 并重新探测；恢复失败写 critical latch、撤销自动 adoption，保存 recovery material。两个文件不能跨文件原子提交，restart journal 在任何候选/CA 调用之前优先恢复，即使进程在第一次 rename 后被 SIGKILL 也可恢复。恢复调用成功退出 3（RECOVERED_ROLLBACK），本次不再请求 CA。

生产路径不可用环境变量重定向到其他产品。仅测试允许 BAOBEI_CERT_TEST_ROOT 指向 root-owned 0700 的 /tmp/baobei-cert-test-*（installer 为 /tmp/baobei-provision-*），其所有可写路径精确受限；测试在一次性 Linux 容器中执行。真实临时 synthetic CA/证书/key 仅在容器 /tmp，ACME、Docker ingress 和网络由 fake 提供，禁止 CA/生产请求。覆盖 SAN/key/有效期、锁、noop、各种 rollback、critical、崩溃/recovery、日志脱敏及 fallback 保持。

node tools/task.mjs baobei cert-test 执行隔离 suite。此检查只运行一次性本地容器，不安装/操作宿主 systemd 或真实 ingress。
