# Phase1-A1.1-R2 成熟平台能力地图

> 目标：能力不是“页面按钮列表”，而是可追溯到业务价值、数据实体、权限、API、Batch 和技术模式的产品契约。

## 1. Project Delivery & Portfolio

| ID | 能力 | R2 | 主要 UI | 核心数据 | 权限 | Batch |
|---|---|---|---|---|---|---|
| PJ-001 | Project Intake | MUST | 新项目登记 Wizard | project_intakes | Portfolio | B2 |
| PJ-002 | Portfolio Overview | MUST | Portfolio 首页 | intake/project aggregate | Portfolio | B2/B4 |
| PJ-003 | 项目生命周期 | MUST | Project Detail | transitions | Portfolio/Project | B2 |
| PJ-004 | Milestone/Activity | MUST | Schedule | milestones/activities | Portfolio | B2 |
| PJ-005 | Resource Calendar | MUST | Resource Center | resource_calendars | Portfolio | B2 |
| PJ-006 | Resource Reservation | MUST | Gantt/Heatmap | reservations | Portfolio | B2 |
| PJ-007 | Conflict Center | MUST | 冲突中心 | schedule_conflicts | Portfolio | B2/B4 |
| PJ-008 | Risk | MUST | Project Risk | project_risks | Portfolio/Project | B2 |
| PJ-009 | Issue | MUST | Project Issues | project_issues | Portfolio/Project | B2 |
| PJ-010 | Change Request | MUST | Change | change_requests | Portfolio/Project | B2 |
| PJ-011 | Commissioning | MUST | Commissioning | records | Project | B9 |
| PJ-012 | Acceptance | MUST | Acceptance | records | Project | B9 |
| PJ-013 | Punch List | MUST | Punch | punch_items | Project | B9 |
| PJ-014 | Handover | MUST | Handover | checklist | Project | B9 |
| PJ-015 | Warranty phase | MUST | Project/Asset | warranty | Project | B5/B9 |
| PJ-016 | Baseline vs Actual | MUST | Gantt | schedule baseline | Portfolio | B2/B4 |
| PJ-017 | Site access window | MUST | Calendar | schedule item | Portfolio | B2 |
| PJ-018 | What-if scenario | LATER | Portfolio | scenario | Portfolio | later |
| PJ-019 | CPM/auto leveling | LATER | — | — | — | later |

## 2. Asset Operations

| ID | 能力 | R2 | UI | 数据 | Batch |
|---|---|---|---|---|---|
| AS-001 | Asset Register | MUST | 行车台账 | asset | B3/B4 |
| AS-002 | Asset Create/Edit/Retire | MUST | Wizard/Drawer | asset | B3/B4 |
| AS-003 | Location hierarchy | MUST | Location | locations | B3/B4 |
| AS-004 | Installation history | MUST | Asset 360 | asset_installations | B3/B6 |
| AS-005 | Equipment Register | MUST | 设备台账 | equipment installation | B3/B4 |
| AS-006 | Equipment Catalog | MUST | 型号目录 | catalog | B3/B4 |
| AS-007 | Equipment Relations | MUST | Topology | relations | B3/B7 |
| AS-008 | Supplier | MUST | Supplier | partner | B5 |
| AS-009 | Warranty | MUST | Warranty | warranty | B5 |
| AS-010 | Spare Part light | MUST | Spare | part catalog | B5 |
| AS-011 | Data Completeness | MUST | Asset Overview | computed | B4/B10 |
| AS-012 | Asset Clone as Draft | LATER | Asset | draft copy | later |

## 3. Realtime Operations

| ID | 能力 | R2 | 说明 |
|---|---|---|---|
| RT-001 | Connectivity | MUST | ONLINE/OFFLINE/UNKNOWN/UNBOUND，runtime owned |
| RT-002 | Shadow | MUST | 当前 X/Y/Z/load/mode 等 |
| RT-003 | Alarm Center | MUST | 现有能力增强 context |
| RT-004 | Alarm ACK | MUST | 留痕，不伪造设备恢复 |
| RT-005 | Alarm→Work Order | MUST | 关联维护闭环 |
| RT-006 | Runtime Binding Viewer | MUST | 业务只读 |
| RT-007 | Remote Control | A2_PLUS | R2 禁止 |

## 4. Digital Twin

- Physical Twin；
- Semantic Equipment selection；
- Equipment Tree/Inspector；
- Select/Focus/Isolate/Hide/X-Ray；
- LiDAR FOV / Camera Frustum；
- Coordinate frame；
- Alarm focus；
- System Topology；
- GLB version/accuracy；
- Live/History frame source；
- Measurement；
- WebGL fallback。

全部 MUST，主要在 B7；高级 Section Clip 可 LATER。

## 5. Maintenance & Reliability

- Inspection templates/records；
- Daily/periodic/MRT rope inspection；
- PM TIME/METER/HYBRID；
- Work Orders；
- Replacement；
- Calibration；
- Software/Firmware；
- Wire Rope 4-model；
- Spare usage；
- Warranty due；
- Alarm maintenance request；
- Attention Center。

## 6. History & Forensics

- Business Revision；
- Lifecycle History；
- Timeline；
- Metric Catalog；
- Time Quality/GAP；
- History API；
- Configuration@T；
- History Explorer；
- Replay；
- Incident Review；
- Revision compare。

## 7. Compliance & Governance

- 使用登记/检验字段载体；
- Daily/Weekly/Monthly records；
- Technical archive completeness；
- Audit；
- Documents；
- Import/Export；
- Global Search；
- Attention；
- Role/capability governance。

## 8. Cross-cutting UX capabilities

- Ctrl+K；
- Recent items；
- URL state；
- Compact/Comfortable；
- Loading/Empty/Error/Partial/No Runtime/No History；
- DirtyGuard；
- 409 conflict；
- Idempotent create；
- 站内通知；
- Export scoped；
- Accessibility。

