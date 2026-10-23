# Crane Operations Platform

全国行车综合管理平台：Vue 3 沙盘 + Go 云端模块化单体 + 出站 Edge + MQTT 5/TLS + PostgreSQL。目标 20 台，大多时间 SILENT。当前 S3.5 UI 数据真实性与运行体验收口；真实设备、生产身份、远程 Web 和点云未启用。

## 快速开始

需要 Node 24.19.0 / npm 11.17.x、Go 1.27.3、Docker Compose。Windows 无需 make。

```sh
node tools/task.mjs bootstrap
node tools/task.mjs dev-up
node tools/task.mjs integration-test
node tools/task.mjs dev-down
```

打开 http://localhost:18080，开发凭据从忽略的 deploy/compose/runtime/.env 中 DEV_API_TOKEN 读取，在页面登录；请勿公开凭据。API 模式首页显示 Cloud Runtime，告警页读取 Cloud Alarm，设备页读取云端 Shadow。SIM 缺少可信地理元数据，不加入地图；空间沙盘仍展示匿名登记 / DEMO 资产。

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

现场自治；云端不直连 PLC；不开放 Edge 入站公网端口。开发身份仅用于本机验证。Secret/证书/生成物不入库，公开数据匿名。旧客户版和完整历史保存在本地忽略的 local-private 与旧分支，公共历史不包含旧客户提交。根开源许可证由所有者决定；第三方条款见 THIRD_PARTY_NOTICES.md。

S3.5 从 main 的 577965363e3a8dd9dec62a0cffafd8aaf330786f 建立 feature/s3-5-cloud-ui-integration。参见 [S3.5 前端语义](docs/04-前端与可视化/S3.5云端数据与会话语义.md) 与 [延期问题](docs/07-测试与验收/S3.5延期问题.md)。完整 CI 通过后保持功能分支，等待人工审查；不创建 PR，不合并 main，不自动部署服务器或进入 Phase 1-A。
