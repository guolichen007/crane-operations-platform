# Phase1-A1.1-R2 状态机、业务事件与通知模型

## 1. 为什么需要显式状态机

成熟平台中的 Project、Work Order、Asset status、Inspection finalization、Handover 都不能是自由下拉。状态变化意味着权限、历史和下一步动作变化。

## 2. Project Transition

transition service 校验 allowed transitions、capability、必要前置条件（例如 blocking punch）。成功后写 Project 状态、transition event、audit、timeline，必要时 Attention。

## 3. Work Order Transition

DRAFT→OPEN→IN_PROGRESS→WAITING→COMPLETED；CANCELLED 为旁路。完成必须满足必要 result/close info。

## 4. Asset Status

DRAFT→ACTIVE→OUT_OF_SERVICE↔ACTIVE→RETIRED；RETIRED 恢复是否允许由后续 policy 决定，默认需要 platform_admin + reason。

## 5. Inspection

DRAFT→FINALIZED；finalized 后不随意修改。如需纠错，创建 correction revision，保留原记录。

## 6. Event 与 Timeline

业务 domain event 不等于消息总线。R2 可在同一事务同步产生 timeline/audit/attention index。

## 7. Attention

Attention 由 canonical 数据计算/索引：due、conflict、alarm。状态仅表示用户是否看过/处理入口，不允许通过 Attention 自己改变 canonical object。

## 8. 站内通知

R2 只站内，不接 email/SMS/WeChat。避免引入模板/送达/隐私复杂度。未来外部通知作为独立 delivery adapter。

