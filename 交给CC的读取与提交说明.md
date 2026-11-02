# Phase1-A1.1-R2 docs 完整方案包：交给 CC 的读取与提交说明

> 角色：本方案包由项目负责人统一整理，目标是把当前 A1.1 的“Premium 展示 + 只读台账 + 通用沙盘”升级为可长期演进的全国行车综合管理平台。
>
> 当前只允许文档治理，不允许借此包直接修改产品代码、SQL migration、contracts、生产服务器或远端 Git。

## 1. 使用方式

1. 将本压缩包解压到仓库根目录之外。
2. CC 先完整阅读本文件、`Phase1-A1.1-R2-成熟综合管理平台主蓝图.md`、`docs/项目管理/Phase1-A1.1-R2-分阶段实施与审查计划.md`。
3. 再按文档索引逐份读取架构、数据、前端、Three、历史、安全与测试文档。
4. 只把 `docs/` 中明确属于 R2 的文档复制/合并到仓库对应目录；不得覆盖与 R2 无关的既有历史文档。
5. 若仓库中存在旧版同名 R2 文档，以本包为“负责人整合版”，但合并前仍需 `git diff`，不能机械覆盖用户后续手改内容。
6. 更新本地 `docs/会话上下文.md`，但该文件继续 **DO NOT COMMIT**。
7. 本轮仅允许 docs commit；提交完成后 STOP，由 OWNER 手动 push，再由负责人从 GitHub 审查。

## 2. Git 边界

- 远程 `main` 基线：`29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18`。
- R2 功能分支：`feature/phase1-a1-1-r2-asset-operations`。
- 不 push `main`；不移动 `baseline/phase1-a1`、`baseline/phase1-a1-1`。
- 不 force push，不 `--all`，不 `--mirror`，不批量 `--tags`。
- 文档提交建议：
  - `docs(架构): 完善A1.1-R2成熟平台完整技术蓝图`
  - `docs(项目): 冻结R2分批实施与审查基线`

## 3. 本包解决的问题

本包不是“功能清单”，而是把以下内容连成一条可执行链：

`产品能力 → 用户工作流 → 数据实体 → API → 权限 → 前端组件 → Three.js语义 → 历史/回放 → 性能 → 测试 → Git审查 → CI → 服务器演练 → 生产迁移`。

尤其补齐：

- Project Intake、Portfolio Scope、项目排期、资源冲突、风险/问题/变更、Commissioning/Acceptance/Handover；
- Asset → Equipment → Runtime Binding → Telemetry 数字资产图谱；
- LiDAR/Camera/PLC/VFD/Motor/Brake/IPC/Switch/传感器/钢丝绳完整档案；
- Point-in-time Configuration Resolver、时间质量、GAP、历史 Replay 与 Incident Review；
- Three.js 从“会动模型”升级为 semantic equipment workspace；
- 高密度 DataGrid、服务端分页、可编辑表单、Ctrl+K、Attention Center、到期中心；
- 分批实现制度：G0-R1、B1～B10，每批 OWNER 手动 push 后必须先过 GitHub/负责人审查。

## 4. 绝对禁止

R2 仍属于 **Phase1-A1.1**，不是 A2。禁止：

- 真实设备 Enrollment；
- 真实设备证书生命周期；
- 公网 Crane MQTT；
- PLC command / Remote Control；
- 修改生产 TLS/DNS/Firebot；
- 在生产服务器试验 migration；
- 将客户地址、真实坐标、账号、Secret、证书、私钥、现场日志、真实 GLB 放入公开仓库。

## 5. 文档读取顺序

1. `Phase1-A1.1-R2-成熟综合管理平台主蓝图.md`
2. `docs/00-项目总览/Phase1-A1.1-R2-从Demo到成熟平台能力路线图.md`
3. `docs/产品设计/Phase1-A1.1-R2-成熟平台能力地图.md`
4. `docs/01-系统架构/Phase1-A1.1-R2-目标架构与领域边界.md`
5. `docs/03-数据模型/*`
6. `docs/04-身份与权限/*`
7. `docs/04-前端与可视化/*`
8. `docs/产品设计/*产品规格.md`
9. `docs/架构/Phase1-A1.1-R2-技术模式与REA来源登记.md`
10. `docs/07-测试与验收/*`
11. `docs/项目管理/*`
12. `docs/08-部署运维与成本/*`

