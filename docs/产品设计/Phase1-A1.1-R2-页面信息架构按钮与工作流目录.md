# Phase1-A1.1-R2 页面信息架构、按钮与工作流目录

> 用于避免“模型设计完整，但前端忘了真正可操作按钮”。所有写按钮必须有后端 capability；这里是 UX 目录，不是安全边界。

## 1. Global

- Project/Portfolio Scope Selector
- Ctrl+K Search
- Attention Center
- Recent Items
- User/Profile/Logout
- Density：Compact/Comfortable

## 2. Portfolio Overview

按钮：新项目登记、打开排期、查看冲突、筛选项目、导出组合摘要（后置可选）。

卡片/列表：在途项目、即将进场、延期、待验收、Blocking conflict、资源过载。

## 3. Project Register

按钮：新登记、查看、编辑 Intake、提交审核、批准/拒绝、Hold/Resume（按状态）、进入正式项目。

不能直接 Delete 已经进入 Review/Approved 的项目登记；使用 CANCELLED。

## 4. Project Schedule

按钮：新增 Activity、编辑计划、分配资源、设依赖、保存 baseline、查看 conflict、打开 resource heatmap。

第一版不必 Gantt 直接拖拽改日期；Drawer 编辑更可靠。

## 5. Conflict Center

按钮：查看详情、调整排期、重新分配、拆分活动、Override（需要 capability）、撤销 Override（如业务允许）。

## 6. Project Detail

- Overview：编辑资料、状态 transition；
- Risk：新增/编辑/关闭；
- Issue：新增/分派/解决；
- Change：提交/审批/拒绝；
- Commissioning：新增测试记录、执行 checklist；
- Acceptance：创建验收、记录结论；
- Punch：新增/指派/关闭；
- Handover：开始移交、对比 planned/actual、确认 AS-BUILT。

## 7. Operations Center

按钮：地图/列表/空间沙盘切换、快速过滤、只看告警、只看离线、只看待检、进入 Site/Asset。

点击 marker：弹出 Site/Asset Card，直接“进入资产”“实时”“历史”，不绕多级页面。

## 8. Asset Center

按钮：新增行车、导入、导出、筛选、列设置、编辑、退役、查看历史。

禁止核心资产直接 Delete。

## 9. Asset 360

Header CTA：编辑资产、新增点检、创建工单。

运行：打开实时、孪生、历史、告警。

资产：添加 Equipment、安装钢丝绳、配置位置、记录软件/标定。

合规：新增记录、上传/关联文档（生产上传开启后）。

## 10. Equipment Center

按钮：安装 Equipment、编辑业务资料、移除、替换、记录标定、记录软件/固件、创建检查、创建工单、查看历史、查看拓扑。

Runtime Binding 仅“查看”，不提供业务编辑。

## 11. Twin Workspace

工具栏：Select、Focus、Isolate、Hide、X-Ray、Reset、Overview/Top/Side/Hoist、FOV、Coordinate Frame、Labels、Measure、Alarm Focus。

Inspector CTA：查看档案、历史、工单、点检、标定、软件版本。R2 无控制按钮。

## 12. Wire Rope

按钮：新增规格、安装钢丝绳、记录检查、记录 MRT、创建工单、更换、查看历史。

不能直接把旧绳字段覆盖成新绳。

## 13. Maintenance

### PM
新增计划、编辑、启用/暂停、查看 next due、生成/打开工单。

### Work Order
创建、分派、开始、等待、完成、取消；添加 inspection/replacement/parts；打开关联 Alarm/Equipment。

### Inspection
执行 checklist、保存草稿、提交 final、创建工单。

## 14. Compliance

新增登记/检验/日周月记录、关联证据、查看到期；界面持续显示“平台记录不替代法定检验结论”。

## 15. History

Time Conductor：LIVE/1H/6H/24H/7D/30D/CUSTOM。

按钮：添加 metric、事件开关、Replay、Revision compare、Incident Review、跳转 Alarm/Work Order。

## 16. Data Governance

Import：上传→Preview→Validate→Conflict→Confirm→Job Result。

Export：选择实体/筛选→提交任务→下载。

Documents：查看/分类/关联；生产上传 FEATURE_OFF 时 CTA 明确禁用原因。

