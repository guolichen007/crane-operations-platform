# Phase1-A1.1-R2 当前状态与交接

> 记录当前进度、环境、边界，供负责人审查与后续开发接手。DESIGN_ONLY=YES，当前批次 G0-R1。

## 1. 阶段状态

| 项 | 值 |
|----|----|
| 阶段 | Phase1-A1.1-R2（成熟综合管理平台） |
| 当前批次 | G0-R1（主计划与能力地图，纯文档） |
| 设计冻结 | R2_DESIGN_FROZEN=YES_AFTER_R2_0_PATCH（5 P1 已收口） |
| 分支 | feature/phase1-a1-1-r2-asset-operations |
| 基线 | origin/main=29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18 |
| 产品代码 | 未开始（G0-R1 只文档） |

## 2. 环境（修正后，勿再写"Go 缺失"）

| 工具 | 状态 |
|------|------|
| Node | 22.23.4 AVAILABLE |
| Go | 1.27.3 AVAILABLE（用户态已装 `/sessions/.../.go/go`） |
| REA | 6.8.0 AVAILABLE（用户态 npm global） |
| Docker daemon | UNAVAILABLE_HARD_LIMIT（NoNewPrivs=1 非特权容器，daemon 起不来） |
| PostgreSQL | USERSPACE_INSTALL_FEASIBLE_NOT_YET_FROZEN（项目用 postgres:17.8；可用户态装 EDB/deb + initdb/pg_ctl） |
| Browser | USERSPACE_PROBE_REQUIRED（Playwright e2e/视觉回归需装 chromium，未探测） |

**验证分工**：CC 本地可跑 contracts 生成 + Go 单测 + migration（装 PG 后）+ 前端 lint/build；Docker Compose 集成（mosquitto/keycloak/postgres）与权威 e2e 归 GitHub CI；生产服务器只做 B10 release rehearsal。

## 3. 已交付文档

- `Phase1-A1.1-R2-成熟资产运营平台设计蓝图.md`（主蓝图，四轮 + 5 P1）
- `Phase1-A1.1-R2-方案整理与实施蓝图.md`（方案总览，将升级为七域）
- `docs/架构/`：行车数字资产图谱、设备与运行数据边界、自动化设备分类与字段模型、前端工业工作台与数字孪生规范、工业历史数据与回放架构、前端性能与Bundle预算、历史数据保留与容量规划、技术模式与REA来源登记
- `docs/产品设计/`：成熟平台对标研究、REA前端优秀平台取证报告、成熟平台能力地图、项目交付与资源排期设计
- `docs/项目管理/`：分阶段实施与审查计划、当前状态与交接
- `AGENTS.md`（阶段=R2，LOCAL_ONLY）

## 4. 关键边界（守住）

- LOCAL_ONLY：只本地 commit，禁 push/PR/merge main/tag。
- 不进入 A2：禁真实设备 Enrollment、证书生命周期、公网 MQTT、远程控制、PLC command。
- 不动生产：禁生产服务器/数据库/TLS/DNS/Firebot；B10 才做 isolated rehearsal。
- ownership 三分：BUSINESS 可编辑，DEPLOYMENT/TELEMETRY 禁 UI 伪造。
- Inventory：已存在后只允许更新 DEPLOYMENT_OWNED。
- 命名：equipment_* 统一，禁 component_*。
- 依赖：不引入 Redis/Kafka/K8s/TSDB/Elasticsearch/React/TresJS/大 UI Framework；AGPL 只学不复制。
- 敏感：credential 永不入库；位置/坐标不进公开 fixture（synthetic 示例）。

## 5. 下一批（B1）启动前置

1. 负责人 G0-R1 审查通过（R2_DESIGN_FROZEN 终审）。
2. 确认 PostgreSQL 用户态安装方案（或由 CI 承担 migration 验证）。
3. 从 G0-R1 HEAD 连续实施 B1（contracts → migrations → ownership → capability → equipment schema → 时间质量持久化）。

## 6. 验收四链（最终盯）

1. 新增行车→真实位置→新增 PLC/LiDAR/Camera/VFD→首页找到→Asset 360
2. 点 Three LiDAR→真实 Equipment→型号/安装/标定→实时→历史→工单
3. 装钢丝绳→检查→异常→工单→更换→旧绳永久保留
4. 选历史 Alarm→Incident Review→T-30→配置@T→X/Y/Z/Load 回放→Equipment/软件/标定@T→Timeline→看清全过程
