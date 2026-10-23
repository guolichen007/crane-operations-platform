# Phase 1-A0 实际资产与数据源模式切换验收

基线：已验收S3.5 / 3b7d4369f2d25800ca4f34f93b645ab3b68b6189。分支：feature/phase1-a0-asset-runtime-mode。验收日期：2026-10-23。

按本阶段任务书的独立授权，main已从T0.5快进到该S3.5提交，没有squash或merge commit。本轮新功能始终在独立分支，不合并main、不创建PR，不连接或部署实验服务器。

## 已完成

- 生成契约新增Inventory/Fleet/项目与资产DTO，新增两个只读API；原MQTT Envelope、10种Topic及既有Schema不变。Go与Web共享类型，无第二套手写Contract。
- 新增00003，保留00001/00002原内容。项目、资产与Runtime三层分离；nullable技术参数、复合外键、来源枚举、同资产同来源唯一约束。导入为事务性幂等upsert，拒绝重复ID、关联错误、来源转换和部分提交。
- 私有Inventory在本地忽略目录保存并校验，只提交匿名example；公开example不配置真实地点或型号组合。三项COMPLETED只登记Project，未制造设备、型号或Telemetry。
- Simulator的--fleet-file优先于旧Count；Runner、Mapper、Cloud订阅和WATCH/LIVE均使用Descriptor。旧SIM-001至020 Count模式完整保留在专用legacy适配中。
- 开发证书与ACL由清单驱动，逐设备精确限定tenant/project/device；新增模拟身份不复用真实身份或证书。不扩大为生产PKI。
- API模式首页、项目/设备台账改读Inventory；运行Shadow独立在/runtime及详情中保留。首页区分规划、完成待接入、模拟在线与真实接入。地图仅对已配置adminCode使用GeoJSON行政区中心；无元数据不造点，未使用现场坐标。Demo/离线及原地图回归保留。
- 精确legacy重置工具要求APP_ENV=development及显式确认，只允许未绑资产的原20个SIMULATION身份；完整清理关联运行表，保留其他测试身份和审计。服务器执行前仍必须另行批准并pg_dump备份，本轮没有执行服务器重置。

## 本地实际门禁

| 入口 | 实际结果 |
|---|---|
| npm run contract | PASS，生成契约无漂移 |
| node tools/task.mjs lint | PASS，漂移、安全预检、Go vet/gofmt/staticcheck、TypeScript、ESLint |
| node tools/task.mjs test | PASS，Go全部普通测试、Web44/44、3组标准Contract解析；外部环境依赖明确skip，由Compose真实执行 |
| node tools/task.mjs build | PASS，Go全包与Demo/API Vite构建 |
| npm run test:e2e | PASS，36/36：原26地图 + 1 Demo告警 + 原6 Cloud + 新3 Inventory；最后页头与匿名行政区测试调整后9项Cloud再次通过 |
| node tools/task.mjs integration-test | PASS，28条真实Compose验收记录，原20设备链路和新10设备资产模式均实际运行 |
| 受保护legacy reset额外数据库反证 | PASS，所有关联表被清理，SIM-021的Shadow与审计保留，生产环境拒绝执行 |
| npm run build:html + 断网file浏览器 | PASS，Demo告警、34个省级汇总、无Cloud API请求 |
| node tools/task.mjs security | PASS，仓库预检、npm audit、govulncheck |
| gitleaks dir --redact | PASS，无泄漏 |
| 客户名称隐私检查 | PASS，9个客户名称在公开待提交文件中0命中，真实Inventory被Git Ignore |

最终HEAD还必须有六个完整CI job全绿：repository-contract/backend/edge/frontend/integration/security，包含Go race、Secret历史扫描、Trivy源码和源码构建镜像扫描。最终CI_RUN_URL与HEAD_SHA/CI_SHA由交付文字提供，必须相同SHA，不以本地结果、单个job或旧提交替代；本报告自身提交也触发完整CI，不使用skip ci。

## 数据与自动证据

匿名与私有Inventory均验证为9项目、10资产、10模拟运行身份。生命周期为5 PLANNED、1 PRE_DEVELOPMENT、3 COMPLETED；三个已完成项目的资产和运行端都是0。运行中的新Fleet严格为SIM-P001-01、SIM-P002-01、SIM-P003-01、SIM-P004-01/02、SIM-P005-01/02、SIM-P006-01/02/03。真实运行端为0。

| 要求 | 自动证据 |
|---|---|
| 空库→3、2→3且保留旧Shadow/来源；幂等、失败回滚、独立REAL身份 | internal/inventory/database_test.go，在真实PostgreSQL执行 |
| 非法字段、生命周期、重复ID、跨项目关联、负载重参数、严格YAML | internal/inventory/inventory_test.go |
| 清单优先、旧20 Count、确切10身份、Mock禁止REAL、ACL确定性及身份隔离 | internal/fleet/fleet_test.go、internal/platform/config_test.go、Inventory示例测试 |
| 10个命名证书CN、沿用CA签叶证书 | tools/certs/main_test.go |
| 实际跨项目拒绝、10台正确Topic、P004 WATCH/LIVE、9/10/10/0 API | tests/integration/run.mjs及tools/diagnostics/mqtt-probe |
| 首页9/6/3/10/10/0、完成待接入不显示故障、空档案不造设备、行政区点 | tests/e2e/cloud/inventory.spec.ts、apps/web-console/tests/inventory.test.ts |
| 20设备、1Hz、5Viewer/1上游、Grace/TTL、三类重启与关键事件ACK | 原完整集成反证与S3.5浏览器用例，全部保留 |

原始结果、截图和CI摘要仅保存在local-private/validation/phase1-a0，原Phase0/T0.5/S3.5证据未删除。新增模式使用独立临时测试数据库，清理后保留原实验库；本轮未删除服务器数据，也未把客户资料写入测试源码、镜像或Git。

## Git整理与后续边界

公开基线以annotated tags保留：baseline/phase0 → bb059256149a99f8e1482b5e77af9403a70bbe80；baseline/t0.5 → 577965363e3a8dd9dec62a0cffafd8aaf330786f；baseline/s3.5 → 3b7d4369f2d25800ca4f34f93b645ab3b68b6189。仅推送这三个显式公共tag。

旧公开功能分支phase0-enterprise-bootstrap、phase0-5-protocol-hardening、s3-5-cloud-ui-integration均已被main完整包含，可在保留tag后删除远端与本地引用。六条早期客户版本地分支的当前HEAD已逐一确认在pre-monorepo.bundle中，bundle验证为完整可恢复历史；仅清理引用，不上传私有bundle、不为客户版推送公开tag。清理的实际结果以最终Git远端/本地核验为准。

已知限制：仍为开发身份与开发CA；未做生产OIDC/PKI、真实PLC/ROS、Remote Web、点云和远控。资产API当前按REAL优先选择一个展示来源，历史模拟端保留独立身份；项目编辑/批量导入UI与正式多租户权限留待后续。公开示例没有业务位置/型号，实际数据必须从私有Inventory加载；不能以匿名示例当作真实现场数据。

SERVER_DEPLOYED=NO。最终同SHA完整CI通过后可提交人工批准服务器资产模式切换；READY_FOR_PHASE1_A_PRODUCTION_FOUNDATION=NO。当前Phase1-A0分支不合并main，PR_CREATED=NO，PR_URL=NOT_CREATED_BY_REQUEST；完成推送和Git整理后停止。
