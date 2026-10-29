# Phase1-A1.1-R1 Production Topology Finalization

基线 f3445f6bb0a912826d9c1e7a2d30ad4f2975608b，继续 feature/phase1-a1-1-premium-ui。未改 main、baseline/phase1-a1、真实服务器、DNS/TLS 或 Firebot，未进入 A2。

## 正式拓扑与测试来源

frontend 是显式命名的 external Docker network，默认 u-dao-crane-frontend，由受控运维创建/维护，Crane Compose down 不删除它。backend 继续 internal；PG/MQTT 无公网端口且不接 frontend，Cloud API 无宿主发布端口。shared ingress 独占80/443，经 Docker DNS 的 web-console:8080、keycloak:8080 转发；127.0.0.1:18081/18082 仅保留宿主运维 fallback。

正式 crane.youdaozd.com 模板采用 resolver 127.0.0.11 valid=5s ipv6=off 与变量 proxy_pass。/realms/、/resources/ 到 Keycloak，/auth/、/api/、/ 到 Web；/admin、/admin/、/health、/metrics 拒绝公网；callback access_log off，通用日志只使用无 query 的 uri。callback 与 Keycloak 路径的 error_log 指向 /dev/null，避免上游失联时错误日志记录 OIDC query；测试同时检查 access log 与入口容器日志。

生产 integration 不再独立手写 routing，直接从该正式模板替换匿名域名、随机 TLS 端口、临时证书路径，以及带测试端口的 Host header。正式 resolver/upstream/location/security/log 配置保持同一来源。匿名测试先执行无上游 nginx -t/启动，再验证真实 HTTPS discovery 和路由；Keycloak/Web 分别销毁重建并占住旧 IP，要求新 IP 不同且入口重解析成功；回调标记不得进入 access log。

测试自行创建带随机名称与归属标签的 external network，env 显式覆盖默认真实网络名；清理前核对创建返回的 ID、名称与标签，只删除自己创建的容器/网络，不连接 u-dao-ingress 或 u-dao-crane-frontend。

## 门禁与身份边界

node tools/task.mjs production-contract 新增正式模板及 Compose 门禁：

```text
PRODUCTION_INGRESS_NO_LOOPBACK_UPSTREAM=PASS
PRODUCTION_INGRESS_DOCKER_DNS=PASS
PRODUCTION_FRONTEND_EXTERNAL_NETWORK=PASS
```

这些字段只有命令实际通过才输出。production-preflight.test.mjs 增加反证：loopback、静态 upstream、错误 resolver、callback 记录、公开管理路由、Compose-owned frontend、非 internal backend、发布 Cloud API 端口与伪造 shared network 归属均拒绝。原有 source/disk/release/身份/安全门禁没有降低。

realm 启用 UPDATE_PASSWORD required action，实际管理员创建由私下部署计划执行，要求临时初始密码及用户级 UPDATE_PASSWORD。不把用户名映射为权限，不增加 platform_admin 全局 scope。三项真实 Keycloak 浏览器用例保留，匿名 scoped 管理员用例补充：改密前 /auth/session=401，完成首次改密后才建立会话，且仍无法跨项目读取。密码在执行时随机生成，不进入 Git、CI 输出或发布 artifact。

所有项目授权必须逐项目显式 grant；首次范围来自负责人批准的私下计划，公共仓库不收录实际账号、密码、身份或授权清单。无数据库变更，无 migration。

## 交付字段与验收依据

阶段字段统一为 HISTORICAL_A0_BASE_SHA、A0_BASELINE_SHA、A1_BASELINE_SHA、CURRENT_MAIN_SHA、A1_1_DELIVERY_SHA；R1 同时报告 A1_1_R1_BASE_SHA 和 A1_1_R1_HEAD_SHA。

本地结果与最终远程结果分开记录。正式交付以本轮 HEAD 对应的八项 CI、真实 Keycloak 3/3、backup/restore、生产模板拓扑及新 release manifest/SBOM 为准，不能沿用 f3445f6 的镜像或结果。最终实际状态由 Actions 和下载校验后的本地交付报告提供。

完成后仅推同一 feature 分支，停在负责人第三轮架构/安全/代码审查点；不自动合并、不部署。

实现依据：[NGINX proxy_pass](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_pass)、[Keycloak 26.8.1 Required Actions](https://www.keycloak.org/docs-api/26.8.1/rest-api/index.html#_requiredactionproviderrepresentation)。
