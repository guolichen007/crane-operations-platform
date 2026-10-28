# Crane Operations Platform

全国行车综合管理平台：Vue 3 沙盘 + Go 云端模块化单体 + 出站 Edge + MQTT 5/TLS + PostgreSQL。当前资产模式为9项目、10规划行车与10模拟运行端，大多时间SILENT；旧20台Count模式保留给回归。当前 Phase1-A1 Production Web Foundation 在功能分支交付 OIDC/BFF、服务端 Session、项目权限与生产部署基础；尚未部署服务器，真实设备、远程 Web 和点云未启用。

当前 UI 工作为 A1.1 Premium，基线 `3525a351aa3677b15754541ff13e9f27e5e0685f`，分支 `feature/phase1-a1-1-premium-ui`。新增品牌登录门、单域名 Keycloak 主题、运营 HUD、四轴数字孪生与自适应画质，身份权限沿用 A1。[实施与验收状态](docs/07-测试与验收/Phase1-A1.1实施追踪.md)和[交付审查说明](docs/08-部署运维与成本/Phase1-A1.1界面交付审查.md)记录实际结果；不自动合并或部署。

## 快速开始

需要 Node 24.19.0 / npm 11.17.x、Go 1.27.3、Docker Compose。Windows 无需 make。

```sh
node tools/task.mjs bootstrap
node tools/task.mjs asset-up
node tools/task.mjs dev-down
# 完整集成回归从legacy Count配置启动
node tools/task.mjs bootstrap
node tools/task.mjs dev-up
node tools/task.mjs integration-test
node tools/task.mjs dev-down
```

打开 http://localhost:18080，开发凭据从忽略的deploy/compose/runtime/.env中DEV_API_TOKEN读取，在页面登录；请勿公开凭据。API模式首页、项目和资产台账使用Inventory API；/runtime与设备详情保持Runtime Shadow/LIVE。SIM明确标为规划模拟，项目只按配置的行政区定位；未配置不造点。三个已完成项目暂不制造设备档案。

真实Inventory只放local-private/inventory/actual-projects.local.yaml；asset-up也可通过INVENTORY_FILE选择输入，无私有文件时使用匿名example。不要上传客户名称、地点与型号组合、证书或运行材料。参见[资产模型](docs/03-数据模型/Phase1-A0资产与运行来源.md)和[开发资产模式](docs/08-部署运维与成本/Phase1-A0开发资产模式.md)。

仅开发既有沙盘：npm ci，然后 npm run dev。构建单文件：npm run build:html，输出 release/全国行车设备运营平台_离线版.html，可发给甲方直接浏览器打开。离线 HTML 为演示，不连接设备。

## 工程与验证

apps/web-console 前端；services/cloud-api 云端；services/remote-gateway 受控入口；edge/agent 出站采集；edge/pointcloud-bridge 协议适配骨架；simulators/device-simulator 模拟器；contracts 协议单一事实源；deploy 部署；tests 跨组件验证；tools 工具。

```sh
node tools/task.mjs lint
node tools/task.mjs test
node tools/task.mjs build
npm run test:e2e
```

同名 Makefile 与 scripts/*.ps1 是等价入口。详见 [文档索引](docs/README.md)、[T0.5 工程说明](docs/01-系统架构/T0.5协议与运行时加固说明.md)、[Phase0 验收证据](docs/07-测试与验收/Phase0验收报告.md)、[贡献规范](CONTRIBUTING.md)。

现场自治；云端不直连 PLC；不开放 Edge 入站公网端口。开发身份仅用于本机验证。Secret/证书/生成物不入库，公开数据匿名。旧客户版和完整历史保存在本地忽略的 local-private 的私有bundle中，公共历史不包含旧客户提交。根开源许可证由所有者决定；第三方条款见 THIRD_PARTY_NOTICES.md。

Phase1-A0 已 fast-forward 封板 main=d827891375cd3665041936890ec21b70f52b2b9c，annotated tag baseline/phase1-a0。A1 从该封板基线建立 feature/phase1-a1-production-web；不自动合并、不部署、不修改真实 DNS/TLS 或 Firebot。如现有权限允许可创建 Draft PR。用户本轮明确跳过私有化，Public 例外不允许引入真实凭据或客户资料。

生产说明见[身份权限模型](docs/04-身份与权限/Phase1-A1生产身份与权限模型.md)、[生产部署手册](docs/08-部署运维与成本/Phase1-A1生产部署手册.md)、[备份恢复](docs/08-部署运维与成本/Phase1-A1备份恢复手册.md)、[安全反证](docs/07-测试与验收/Phase1-A1安全反证矩阵.md)与[威胁模型](docs/09-安全/Phase1-A1威胁模型.md)。验证入口：node tools/task.mjs production-contract、a1-test、a1-production-test；Windows 本地候选镜像使用 node tools/build-production.mjs，正式镜像仅认完整 CI 的 release-manifest.json 与 registry digest。

已完成公开基线以baseline/phase0、baseline/t0.5、baseline/s3.5、baseline/phase1-a0四个annotated tag保留；已被main包含的旧功能分支可清理。早期客户版历史仅保留在本地私有bundle，不能推送其tag或历史；禁止git push --all、--mirror或批量--tags。
