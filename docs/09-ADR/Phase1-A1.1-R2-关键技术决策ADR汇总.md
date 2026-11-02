# Phase1-A1.1-R2 关键技术决策 ADR 汇总

## ADR-01 保持 Go 模块化单体

**决定**：不拆微服务。原因：当前规模、事务一致性、安全与部署复杂度都更适合单体模块化。未来按负载证据再拆。

## ADR-02 PostgreSQL 继续承担 R2 数据

业务、Revision、Timeline、Telemetry 继续 PG；不提前引入 TSDB/Elasticsearch。真实 rows/day、disk growth、query P95 达到 trigger 后另 ADR。

## ADR-03 Asset / Equipment / Runtime 分层

业务资产不等于产生遥测的运行身份。借鉴 ThingsBoard/SiteWise/AAS/OPC Machinery 语义，但不实现其平台/协议栈。

## ADR-04 Equipment 取代新 component_* 数据模型

PLC/LiDAR/Camera/IPC/Switch/VFD/Motor 等全部使用 Equipment；钢丝绳例外保持专用模型。

## ADR-05 Portfolio Scope 与 Project Scope 分离

解决未创建项目的登记/排期权限，不给 platform_admin 全局 bypass。

## ADR-06 计划配置与 AS-BUILT 分离

Project planned equipment 只有 Handover 后才进入正式 Asset/Equipment 配置。

## ADR-07 Vue 3 保留，不迁 React/TresJS

现有代码、测试和 Three 底座已经稳定。成熟化重点是信息架构和数据闭环，不是框架重写。

## ADR-08 Pinia / TanStack Query / RuntimeFrame 三分

Pinia 管客户端工作区；TanStack Query 候选管 server state；RuntimeFrame 管高频 telemetry，避免响应式风暴。

## ADR-09 TanStack Table/Virtual + Reka UI

Headless 保持 U·DAO 视觉控制，同时获得高密度表格和可访问 primitive。Virtual 不替代 server pagination。

## ADR-10 ECharts core 按需 + Three opt-in

不允许首页默认承担 970KB Three 与 >1MB charts baseline。Operations default 轻量；Twin/History 进入才加载。

## ADR-11 Three WebGL 生产

R2 不追 WebGPU。GLTFLoader + Meshopt/KTX2；Draco 只有实测收益后采用。

## ADR-12 AGPL 只学模式

realvirtual/xeokit/Grafana 等涉及 AGPL 的代码不进入项目；REA 只记录交互/架构模式。

## ADR-13 历史必须具有时间质量

source time、receive time、boot、sequence、gap/clock skew；Replay 不跨数据 gap 伪造连续轨迹。

## ADR-14 DB-backed jobs 而非 Redis/Kafka

导入/导出等异步任务采用 PostgreSQL job queue，保持基础设施克制。

## ADR-15 Scheduling 采用 PG range + Go conflict engine

硬独占资源可使用 range/GiST/exclusion；可超配/override 冲突由 Go deterministic engine 处理，approval transaction 重检。

