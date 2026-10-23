# Phase1-A0 服务器切换前工程收口

基线：`5d9f57bf8ddc828a684aedbbb107d0912478bfcc`。分支：`fix/phase1-a0-runtime-hardening`。本地验收日期：2026-10-23。

## 变更边界

只加固 Runtime 预注册、Fleet/DB 来源一致性、asset-up 启动范围与私有文件权限，并固定 CI 名称。不修改 Inventory 三层模型、契约、数据库 migration、UI 或边缘自治边界，不进入 Phase1-A。

- Store 首包自动建档白名单精确限定为 SIM-001..SIM-020 和 TEST-001。其他未知身份返回未注册错误，事务不提交，也不写入 Shadow 缓存；已注册身份不可用不同 project 写入。
- Cloud 显式 Fleet 在 Inventory 导入之后、MQTT 与 HTTP/readiness 启动之前，以只读 repeatable-read 快照逐项核对 runtimeDeviceId、projectId、sourceMode、assetDeviceId 与 tenant。缺少登记、空资产关联不一致或来源不符均退出，不降级为 TEST。
- asset-up 明确传入 postgres、mosquitto、cloud-api、web-console、device-simulator；同时检查 Docker Compose 解析后的依赖闭包，出现额外依赖即拒绝。保留 Remote Gateway 和 PointCloud 的源码。
- 写任何私有 runtime 文件前收紧目录 0700，既有 Inventory/Fleet/ACL/.env 文件 0600；新 Inventory 以 0600 写入，Go 生成器以 0600 创建 Fleet/ACL。保留 Git/Docker 两套忽略规则，不打印 Inventory 内容。
- 工作流名为 `Crane Platform CI`，concurrency group 为 `crane-ci-${{ github.ref }}`。

## 本地实测

| 验证 | 结果 |
| --- | --- |
| 契约生成漂移、OpenAPI、lint、Go vet/staticcheck、Web 类型与 ESLint | PASS |
| Go 全部包单测；Web 单测 | PASS；Web 44/44 |
| REST/MQTT 契约与资产启动工程测试 | PASS，6/6 |
| Go 与 Web 构建 | PASS |
| 全部 Playwright E2E | PASS，36/36 |
| Compose 真 PostgreSQL/MQTT5 TLS/浏览器集成 | PASS，Windows 31 条验收记录 |
| npm audit、govulncheck、gitleaks 脱敏扫描、actionlint | PASS |
| local-private/runtime Git 跟踪文件 | 0；两目录均被 Git/Docker Ignore 排除 |

真实 PostgreSQL 反证运行 `services/cloud-api/internal/app/provenance_test.go`，不是 SQL mock：

1. Fleet 首个 SIM-P001-01 未注册，真实 Cloud Run 返回未注册错误。
2. 最后一个 Runtime 未注册也拒绝，防止只验证首项。
3. DB project 不同、source_mode 不同、asset 为空/错误、tenant 不同均拒绝；错误必须来自来源门禁，不能以之后缺少 MQTT 证书的错误冒充通过。
4. 正确 Inventory → Fleet 校验通过，已注册 SIM/EDGE 身份可更新 Shadow；未注册 SIM/EDGE 首包不建档。
5. SIM-001..020 和 TEST-001 保留原来源与空资产关联；随后完整执行原 20 台在线、故障、租约及 MQTT 回归。

集成末段实际调用 `node tools/task.mjs asset-up`，同时断言 Compose 解析依赖集合和最终 running service 集合均恰为五项。9 项目/10 资产/10 模拟/0 真实、跨项目 ACL、WATCH/LIVE 与真实浏览器台账断言全部保留。测试显式使用匿名 example，不读取本地客户 Inventory。

Windows 不具有相同的 POSIX owner/group mode 语义，不将其 mode 位作为 Unix 权限证据。Linux CI 另对实际 asset-up 生成目录和四个文件检查 0700/0600，并运行宽松旧权限收紧测试；因此 Unix 集成为 32 条记录。最终交付必须以与最终 HEAD 一致的六项完整 CI 全部成功为准，CI SHA 和 URL 在交付回复中记录。

## 复跑与限制

首次本地集成切换后，复用的旧 nginx 缓存 Cloud 地址，导致最后页面断言失败；测试前置已恢复原重建 Web 的语义，未降低断言或增加超时。一次直接复跑未重建 broker，仍加载上一模式 ACL；按 dev-down 保留卷、重建测试栈后完整 31 条通过。两模式切换的这项操作前置已写入开发资产模式文档。

保留开发身份和开发 CA。没有 OIDC、生产 PKI、REAL Adapter、Remote Web、PointCloud、Active Source 状态机或新数据库实现。仅功能分支交付，不部署服务器、不合并 main、不创建 PR；下一步等待人工检查同 SHA CI 后单独授权服务器切换。
