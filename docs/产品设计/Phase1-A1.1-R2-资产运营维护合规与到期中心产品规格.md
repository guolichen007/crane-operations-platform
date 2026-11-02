# Phase1-A1.1-R2 资产运营、维护、合规与到期中心产品规格

## 1. Asset Center

支持 List/Card；默认 DataGrid。主要按钮：新增行车、导入、导出；行操作：查看、编辑、退役。

列：asset_code、名称、Project/Site、类型、品牌/型号、载荷、跨度、asset_status、Connectivity、Alarm、最近维护、下次检查、Data Completeness。

## 2. Asset Create Wizard

1 身份；2 制造/型号；3 技术参数；4 合规字段；5 位置；6 初始 Equipment；7 文档 metadata；8 Review。

可保存 DRAFT，不强迫一次填完。

## 3. Equipment Center

统一管理 LiDAR/Camera/PLC/VFD/Motor/Brake/IPC/Switch/Sensor。

按钮：安装 Equipment、编辑业务信息、记录标定、记录软件版本、创建点检、创建工单、移除/更换。

不能通过页面修改 ONLINE/REAL identity。

## 4. Equipment Inspector

显示：nameplate、安装位置、行政状态、实时连接、Condition、firmware/software、标定、最近告警、开放工单、文档、历史。

来源字段要明确：Business / Runtime / Derived。

## 5. Wire Rope Center

台账：Asset/机构/位置/品牌/型号/直径/批次/SN/安装时间/运行量/最近检查/下次检查/status。

详情：Specification / Installation / Inspection History / Replacement History / Documents。

## 6. Maintenance Center

Overview：Overdue、Upcoming、Open Work Orders、Critical Findings、Calibration Due、Warranty Expiring。

PM 可以基于时间/运行量；Work Order 支持 Asset/Equipment/WireRope context。

## 7. Compliance Center

分为：使用登记、定期检验、自行检查、日周月、技术档案完整性。必须标示“系统记录/提醒，不替代法定结论”。

## 8. Attention Center

统一类型：PROJECT_SITE_ENTRY、PROJECT_DELAY、PROJECT_CONFLICT、PROJECT_ACCEPTANCE_DUE、INSPECTION_DUE、MAINTENANCE_DUE、CALIBRATION_DUE、WARRANTY_EXPIRING、DOCUMENT_EXPIRING、ALARM_OPEN。

Attention row 能直接进入 canonical object，而不是单独维护一套真相。

## 9. 重要按钮规范

- Create：新增行车/Equipment/工单/检查；
- Record：标定/软件版本/合规记录；
- Transition：退役/移除/完成/移交；
- View History：任何重要对象可看 Revision/Timeline；
- Export：受 Scope 控制；
- Delete：核心历史对象原则上不出现，使用 Retire/Disable/Cancel。

