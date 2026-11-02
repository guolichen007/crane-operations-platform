# Phase1-A1.1-R2 项目交付、资源排期与 Handover 产品规格

## 1. Portfolio Overview

KPI：登记中、规划中、执行中、Commissioning、待验收、延期、Blocking Conflict、本月进场、本月移交。

主区域：
- Project timeline；
- Resource heatmap；
- Attention；
- 最近变更；
- 高风险项目。

按钮：`新项目登记`、`查看排期`、`冲突中心`。

## 2. 新项目登记 Wizard

Step 1 基础：intake code、名称、客户、project_type、priority。

Step 2 Site：省市/现场文字、Site reference、是否新 Site、现场时区。

Step 3 范围：行车数量、改造/新建/算法/更换/服务、预计 Equipment 类别。

Step 4 时间：requested start/handover、site access window、停机窗口。

Step 5 团队：PM、technical lead、site lead、team/resource requirements。

Step 6 风险：是否需停产、网络条件、资料缺失、客户窗口、现场限制。

Step 7 Review：校验、冲突预检查、提交。

registered_at 系统生成，提交后进入 REVIEW。

## 3. Project Detail

一级：Overview / Schedule / Resources / Risk&Issues / Change / Site / Planned Assets / Commissioning / Acceptance / Handover / Documents / History。

Overview 显示：状态、计划/实际、关键 milestone、负责人、风险、冲突、Attention、最新活动。

## 4. Gantt / Calendar

ECharts Custom Series：
- baseline 灰；
- current plan 蓝；
- actual 绿色/红色延期；
- milestone diamond；
- conflict 红色 overlay；
- dependency 轻量线/tooltip。

第一版不要求完整 drag-to-reschedule；编辑可以 Drawer 表单，避免复杂图形编辑器拖慢实现。

## 5. Resource Heatmap

行：人员/团队/场地/测试设备；列：日/周。显示 demand/capacity。点击 cell → reservation list → conflict details。

Exclusive resource 显示占用；capacity resource 显示利用率。

## 6. Conflict Center

Filter：BLOCKING/WARNING/INFO、resource type、project、date。

Conflict card：冲突原因、两个活动/资源、时间、影响。操作：调整日期、换资源、取消 reservation、override。

Override 必须 reason，UI 显示“此冲突已被谁在何时受控覆盖”。

## 7. Risk / Issue / Change

### Risk

概率×影响矩阵，owner、mitigation、next review。

### Issue

severity、owner、due、status、resolution。

### Change Request

展示 scope/schedule/asset impact。批准后自动产生 Project Timeline event；涉及计划时触发排期重算/冲突检测。

## 8. Commissioning

Checklist template + records。典型项：PLC I/O、网络、LiDAR/Camera mount、标定、限位、安全联锁、报警、软件版本、运行测试。

每项：PASS/FAIL/NA、执行人、时间、备注、evidence document。

## 9. Acceptance / Punch

Acceptance summary + Punch list。Punch 按 BLOCKING/MAJOR/MINOR；blocking 未关禁止 Handover。

## 10. Handover

Handover 页面同时展示 Planned vs Actual：

| 项 | Planned | Actual |
|---|---|---|
| LiDAR 型号 | A | B |
| Camera SN | 预计空 | 实际 SN |
| Software | planned version | installed version |
| Location | design | surveyed/calibrated |

确认后写 AS-BUILT，记录 handover actor/time/reason/document。

