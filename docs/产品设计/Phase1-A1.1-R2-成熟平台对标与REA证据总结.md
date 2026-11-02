# Phase1-A1.1-R2 成熟平台对标与 REA 证据总结

## 1. 研究目的

不模仿视觉皮肤，而是提炼：用户怎么找对象、怎么编辑、怎么下钻、怎么切实时/历史、3D 如何与属性/告警/维护连接、台账如何组织、排期如何发现资源冲突。

## 2. realvirtual-WEB

公开产品展示了 browser-based 3D HMI：3D 机器单元、KPI overlay、设备信息、文档/维护上下文和实时状态。其公开仓库为 AGPL-3.0。

采纳：semantic node、设备 Inspector、runtime binding、KPI/Alarm overlay、GLB 语义。

拒绝：源码复制、AGPL SDK 引入、游戏化过度特效。

## 3. Autodesk Tandem

公开资料突出 3D 模型与 sensor streams、charts、dashboard、asset data 同工作区，并强调数据完整性/数据 dashboard。

采纳：3D+属性+历史并排；Data Completeness；空间对象快速定位。

拒绝：照搬建筑/BIM 信息架构。

## 4. OpenRemote

Manager UI 有 Map、Assets、Rules、Insights；资产树左侧，属性/Location/History 右侧；有权限字段可编辑，sensor/agent 数据可只读；地图资产点击可进入详情。

采纳：Asset Tree/Detail、位置→详情快速进入、字段读写来源明确、History Panel。

## 5. ThingsBoard

官方明确 Asset 与 Device 分离；Relations 表达层级；设备详情包含 attributes/latest telemetry/alarms/events/audit/version control。

采纳：Asset/Equipment/Runtime 分层、Relations、当前+历史+审计并列。

## 6. Open MCT

Time Conductor 把实时和固定历史时间统一为操作上下文。

采纳：Contextual Time Conductor、历史 Replay、多个 panel 同时间窗。

拒绝：Open MCT 框架本体。

## 7. Primavera Cloud

Portfolio Resource Planning 公开文档支持 time-phased role demand/availability、调整 allocation start、scenario/项目选择、overallocation。

采纳：Portfolio、resource demand/capacity、Heatmap、Conflict、Baseline、Scenario 概念。

R2 拒绝：自动优化/CPM/leveling，先做 deterministic scheduling。

## 8. Maximo

PM 支持 time/meter/hybrid、fixed/floating、hierarchy；Asset/Location/Work Order 强关联。

采纳：PM 模型、Asset/Location 分离、Work Order 关联、到期。

## 9. SAP EAM

维护流程有 Planning/Approval/Preparation/Scheduling/Execution/Completion 等阶段。

采纳：显式状态机、审批/执行阶段，不做自由 status dropdown。

## 10. Konecranes / Demag / 国内行车平台

采纳：设备档案+维护/检查/文档统一；running hours、loads、motor starts、work cycles、emergency stop、brake/inverter 等运行指标；钢丝绳独立监测/检查对象；台账导出。

拒绝：未验证寿命预测、OEM 专有硬件算法。

## 11. REA 证据纪律

每次研究必须区分 OBSERVED/PUBLIC_DOCUMENT/INFERENCE；第三方原始 bundle、cookie、session 不提交；public SaaS 不绕过登录。B10 对我们自己 A1.1 vs R2 做最终 route/DOM/network/bundle/screenshot 对比。

