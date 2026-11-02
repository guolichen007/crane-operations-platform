# Phase1-A1.1-R2 成熟综合管理平台主蓝图

> **阶段**：Phase1-A1.1-R2
>
> **目标**：把当前平台从“Premium 展示、只读台账、项目行政区沙盘、通用四轴行车模型”升级为可支撑项目交付、资产全生命周期、实时运营、数字孪生、维保可靠性、历史取证和合规治理的全国行车综合管理平台。
>
> **A2_STARTED=NO**。本轮不做真实设备 Enrollment、真实设备证书、公网设备 MQTT、远程控制和 PLC 指令。

---

## 1. 产品最终定义：七大业务域 + 三个技术底座

### 1.1 七大业务域

1. **项目交付与资源排期**：新项目登记、组合视图、里程碑、施工窗口、人员/团队/现场/测试设备排期、冲突、风险、问题、变更、调试、验收、整改、移交、质保。
2. **资产运营**：行车、位置、Equipment、目录、供应商、质保、轻量备件、文档、当前配置与历史安装关系。
3. **实时运行**：在线状态、运行模式、X/Y/Z/抓斗、载荷、运行时间、工作循环、故障、告警、实时设备健康。
4. **数字孪生**：Physical Twin、Semantic Equipment、传感器 FOV、系统拓扑、实时状态、设备 Inspector、历史 Replay。
5. **运维可靠性**：点检、预防性维护、工单、钢丝绳、部件替换、标定、软件/固件、备件用量、到期提醒。
6. **历史取证**：Revision、Lifecycle History、Timeline、Telemetry History、Configuration@T、Replay、Incident Review。
7. **合规治理**：使用登记/检验字段载体、日管控/周排查/月调度记录、技术档案、审计、数据完整性、导入导出。

### 1.2 三个技术底座

- **Identity / RBAC / Audit**：OIDC、Session、CSRF、Project Scope、Portfolio Scope、capability、审计。
- **Data / Search / History**：PostgreSQL、分页、搜索、时态模型、telemetry、聚合、Revision、Timeline。
- **Documents / Import / Export / Jobs**：文件元数据、storage interface、导入预览、导出任务、DB-backed background jobs。

---

## 2. Demo 与成熟平台的本质差异

| 当前 A1.1 特征 | R2 成熟平台要求 |
|---|---|
| 首页偏展示 | 首页是运营入口：项目、资产、告警、到期、冲突、最近访问 |
| Project-centric | Project + Site + Asset + Equipment + Work + History 多工作域 |
| 资产只读 | 项目范围内可创建/编辑/退役业务资产，运行身份只读 |
| 设备字段少 | 机械/控制/感知/计算/网络/安全 Equipment 完整档案 |
| 行政区中心示意 | Site/Workshop/Runway/Asset 位置层级，真实坐标与 LOCAL_SITE 并存 |
| Three 是展示 | Three object 可选中真实 Equipment、看实时/历史/工单/标定 |
| 只有当前状态 | Current + Revision + Lifecycle + Event + Telemetry + Aggregate |
| 告警孤立 | 告警可确认、关联 Equipment、创建工单、进入 Incident Review |
| 无项目资源计划 | Project Intake + Scheduling + Resource Conflict + Handover |
| 无长期数据治理 | 乐观并发、幂等、审计、Revision、导入作业、配置 at T |

---

## 3. 数字资产主轴

```text
Project / Site / Location
        │
        ▼
Asset（CRANE）
        │
        ├── Asset Installation（位置历史）
        │
        ▼
Equipment Installation
        │
        ├── Equipment Relations（控制/驱动/网络/安装/感知）
        ├── Calibration History
        ├── Software/Firmware History
        ├── Inspection / Work Order / Document
        │
        ▼
Runtime Binding   ← DEPLOYMENT_OWNED
        │
        ▼
Telemetry / Shadow / Alarm ← TELEMETRY_OWNED
```

钢丝绳由于规格、安装、检查、替换和安全语义特殊，保留专用模型，不塞入 generic Equipment。

---

## 4. 数据所有权三分

### DEPLOYMENT_OWNED

不可由业务 UI 修改：内部身份、tenant/project/asset 绑定、runtimeDeviceId、sourceMode、REAL/SIMULATION provenance、MQTT identity、未来证书身份、网络部署敏感信息。

### BUSINESS_OWNED

由 capability 控制编辑：显示名、制造商、品牌、型号、序列号、技术参数、位置、Equipment、钢丝绳、点检、工单、标定、软件版本、合规字段、文档元数据。

### TELEMETRY_OWNED

只能由 Edge/Runtime 更新：在线、heartbeat、X/Y/Z、load、runtime、cycle、fault、alarm、condition telemetry。

**原则**：未接入数据必须显示 `UNKNOWN/UNBOUND/UNAVAILABLE`，不得为了 UI 好看手工填绿色“正常”。

---

## 5. 项目交付主轴

```text
Project Intake
  → Review
  → Planning
  → Approval
  → Formal Project
  → Survey / Design / Preparation
  → Site Execution
  → Commissioning
  → Trial Run
  → Acceptance
  → Punch Close
  → Handover
  → Warranty
  → Closed
```

项目中的 planned equipment 只是计划。只有 Handover 经过实际型号/SN/安装位置/软件/固件/标定确认后，才进入正式 AS-BUILT Asset Configuration。

---

## 6. 分批实现路线

- **G0-R1**：完整 docs、能力地图、技术来源、治理、Batch Gate。
- **B1**：数据/Contracts/RBAC 地基，最多先做 00005/00006 foundational migration。
- **B2**：Portfolio、Project Intake、Scheduling、Resource Conflict。
- **B3**：Asset/Location/Equipment/Catalog 后端 CRUD。
- **B4**：Industrial Shell、DataGrid、Forms、Search、Operations Map、基础可编辑前端。
- **B5**：钢丝绳、点检、PM、工单、标定、软件/固件、供应商/质保、合规、轻量备件。
- **B6**：Revision、Timeline、temporal relation/binding、ConfigurationResolver、History API。
- **B7**：Asset 360、Semantic Twin、GLB、LiDAR FOV、Camera Frustum、Topology。
- **B8**：History Explorer、Time Conductor、Replay、Incident Review。
- **B9**：Commissioning/Acceptance/Punch/Handover、Import/Export、Documents、Project→Asset。
- **B10**：性能、A11y、安全、REA before/after、迁移演练、备份恢复、发布候选。

**每批必须 STOP**：CC 本地提交 → OWNER 手动 push → GitHub CI → 负责人独立审查 → PASS 才进入下一批。

---

## 7. 四条业务验收链

### 链 A：项目到资产

新项目登记 → 排期 → 冲突检测 → 现场执行 → Commissioning → Acceptance → Punch Close → Handover → 正式 Asset/Equipment。

### 链 B：资产到设备

新增行车 → 配置 Site/Workshop/Runway → 新增 PLC/LiDAR/Camera/VFD/IPC → 首页找到 → Asset 360 → 设备档案可编辑、运行身份只读。

### 链 C：可靠性闭环

安装钢丝绳/Equipment → 检查/维护计划 → 异常 → 工单 → 使用备件 → 更换 → 老件历史保留 → Timeline/Revision 可追溯。

### 链 D：事故取证

选择历史 Alarm → Incident Review → T-30min~T+10min → Configuration@T → 当时 Equipment/软件/标定/位置 → X/Y/Z/Load 历史 → GAP 检测 → Three Replay → Timeline → 看清事件全过程。

---

## 8. 技术原则

- 保留 Go 模块化单体，不拆微服务；不引入 Redis/Kafka/Kubernetes。
- PostgreSQL 继续作为业务、历史和当前 R2 telemetry 主存储；真实规模触发前不换 TSDB。
- Vue 3 + Pinia + Vue Router 保留；建议 B4 评估/引入 TanStack Query 管 server state，避免 Pinia 混入请求缓存。
- TanStack Table/Virtual 管高密度台账；Reka UI 管无样式可访问 primitives；样式继续 U·DAO。
- Three.js + camera-controls + postprocessing + GSAP 保留；WebGL 为生产渲染路径。
- ECharts 改 `echarts/core` 按需注册；History/Portfolio timeline 按路由 lazy。
- AGPL 项目（realvirtual、xeokit）只学模式，禁止复制源码进入产品。
- Open MCT 只学 Time Conductor / historical replay 模式。

---

## 9. 生产边界

B1～B9 默认不改生产服务器。B10 才允许 production preflight：

`正式 backup → isolated restore DB → migration rehearsal → old app compatibility → isolated R2 stack → browser/security/performance acceptance → release approval → 正式 migration/deploy`。

shared ingress / Firebot 继续作为相邻业务反证对象，禁止开发期碰生产拓扑。

---

## 10. 成熟平台完成定义

R2 不是“页面都做完”。只有同时满足以下条件才可宣布成熟化完成：

- 核心业务可增删改查且权限正确；
- 项目/资源排期能识别冲突并留痕；
- Asset/Equipment/Location/钢丝绳/工单/合规历史不丢；
- Three.js 是真实 Equipment 业务入口，不是装饰；
- 历史 Replay 不伪造断流轨迹，配置按历史时间解析；
- viewer/maintainer/platform_admin 权限边界由后端反证；
- 页面在 1366×768 与 1920×1080 可用；
- 10k 台账、30d history、20 route cycles、WebGL fallback 等性能门通过；
- fresh DB + 00004→R2 migration、backup/restore、old app compatibility 通过；
- REA before/after 证明信息架构、bundle、交互和业务闭环确有提升。

