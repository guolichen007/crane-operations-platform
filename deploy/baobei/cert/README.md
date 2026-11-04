# Baobei 独立 HTTP-01 证书事务

代码/测试/模板交付；ACME_SERVER_APPLIED=NO，S1B_PUBLIC_DEPLOY=NOT_AUTHORIZED。本轮不运行 installer、bootstrap/adopt、systemctl、真实证书或服务器操作。源自 Crane O1-1 事务设计的独立副本，Crane 原实现未修改。

固定 DOMAIN=baobei.youdaozd.com；ACME_HOME=/srv/u-dao/secrets/baobei/acme；WEBROOT=/srv/u-dao/platform/ingress/acme-webroot；TLS_DIR=/srv/u-dao/platform/ingress/tls/baobei（脚本内部 PRODUCTION_TLS）。单位 u-dao-baobei-cert-renew.service/.timer。共享入口容器名为 u-dao-ingress-nginx-1。

固定 acme.sh 3.1.6，official signed tag + tag object + commit + allowed_signers SHA256 + acme.sh SHA256 全部验证，独立只读 vendor /opt/u-dao/vendor/baobei/acme.sh/3.1.6。无 curl|sh、自动升级或浮动最新版。provision-acme.sh 安装前拒绝运行中的 service、启用的 timer、已持有的 renewal lock；安装只 daemon-reload，不启用、不启动、不签发、不改 TLS。

宿主机 Baobei ACME 在 WEBROOT 的 `.well-known/acme-challenge/` 写入普通文件。只有 baobei-web 将 WEBROOT 只读挂载到 `/var/www/acme`，Nginx worker 读取 token 文件。共享 ingress 把 HTTP-01 路径代理至 `baobei-web:8080`，复用已有的 TLS 根挂载 `/etc/nginx/tls`。此方案无需为共享 ingress 添加 webroot Volume 或重建容器。

后续部署获得独立授权后，准备宿主 webroot、root-owned 私有 state/lock 与可信的人工初始 live pair。启动 Baobei Web，在运行中的共享 ingress 接入独立 frontend 网络，验证 Nginx 配置并 reload。安装脚本验证固定版本后部署到本地。自动签发必须等域名的公网 HTTP challenge 实际可达。--bootstrap 校验临时 token 响应体及公网 IP 后才调用 CA，候选证书进入私有 stage。--adopt 经单独批准后推广，timer 的 --run 只处理已接管状态。--check 检查当前证书及服务。接管和启用 timer 分别授权。

人工 TrustAsia bootstrap-fullchain.pem/bootstrap-privkey.pem 是不可覆盖的 bootstrap/emergency fallback。live 文件名仅 fullchain.pem/privkey.pem；初始复制或紧急恢复需负责人另行授权并保持 key 0600，不将 bootstrap-* 作为自动续签目标，不把 CA/key material 放入 Git。回退使用事务前 live pair 的私有备份，不覆盖人工 fallback。

候选要求精确唯一 DNS SAN、私钥公钥匹配、RSA2048、Let's Encrypt issuer、可信链/hostname/有效期验证且至少剩 7 天。过期的旧 live pair 不阻止合法新候选。证书 PEM 中禁止混入私钥。
推广前 nginx -t；先持久化 rollback pair 和 promotion journal，再原子替换 live 文件、fsync、nginx -t/reload、HTTPS 首页/ready JSON 与远端证书指纹/可信链探测。失败恢复旧 pair 并重新探测；恢复失败写 critical latch、撤销自动 adoption，保存 recovery material。两个文件不能跨文件原子提交，restart journal 在任何候选/CA 调用之前优先恢复，即使进程在第一次 rename 后被 SIGKILL 也可恢复。恢复调用成功退出 3（RECOVERED_ROLLBACK），本次不再请求 CA。

生产路径不可用环境变量重定向到其他产品。仅测试允许 BAOBEI_CERT_TEST_ROOT 指向 root-owned 0700 的 /tmp/baobei-cert-test-*（installer 为 /tmp/baobei-provision-*），其所有可写路径精确受限；测试在一次性 Linux 容器中执行。真实临时 synthetic CA/证书/key 仅在容器 /tmp，ACME、Docker ingress 和网络由 fake 提供，禁止 CA/生产请求。覆盖 SAN/key/有效期、锁、noop、各种 rollback、critical、崩溃/recovery、日志脱敏及 fallback 保持。

node tools/task.mjs baobei cert-test 执行隔离 suite。此检查只运行一次性本地容器，不安装/操作宿主 systemd 或真实 ingress。
