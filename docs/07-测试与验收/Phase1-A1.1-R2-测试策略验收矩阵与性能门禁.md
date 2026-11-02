# Phase1-A1.1-R2 测试策略、验收矩阵与性能门禁

## 1. 三层证据

- **LOCAL_EVIDENCE**：CC 本地 contract/Go/unit/frontend/build/可运行 PG/browser；
- **BATCH_AUTHORITATIVE_EVIDENCE**：OWNER push 后 GitHub CI；
- **PRODUCTION_EVIDENCE**：B10/release 时服务器 isolated rehearsal + 正式验收。

本地 PASS 不能代替 CI PASS；未运行必须 NOT_RUN。

## 2. 每批通用 Gate

### Code

contract、lint、unit、typecheck、build。

### Data

fresh migration、00004→当前 migration、idempotent migration、old app additive compatibility。

### Security

viewer write denied、cross-project denied、CSRF、capability、audit、no secret fixture。

### Browser

1920×1080、1366×768；Compact/Comfortable；slow API；empty/partial/offline；Reduced Motion。

## 3. B1

- Contract codegen determinism；
- 00005/00006 migration；
- ownership counterproof；
- Equipment schema validation；
- telemetry old/new compatibility；
- no global bypass。

## 4. B2

- Intake state transition；
- Portfolio vs Project authorization；
- resource overlap/capacity；
- timezone/calendar；
- dependency violation；
- concurrency：两个 schedule approval 同时提交仅一个合法结果；
- override audit。

## 5. B3

- Asset/Location/Equipment CRUD；
- version/If-Match 409；
- Idempotency-Key duplicate create；
- retire not hard delete；
- parent/child same asset validation；
- server-side paging/sort/filter；
- N+1 counterproof。

## 6. B4

Playwright：Admin 新增行车→位置→Equipment；Viewer 无 CTA + 后端 403；搜索；地图快速进入；DirtyGuard；409 UI；URL state/back；10k equipment mock/server dataset。

## 7. B5

- Wire rope install→inspect→replace；
- old rope retained；
- PM time/meter/hybrid due；
- work-order state machine；
- alarm→work order；
- calibration/software history；
- compliance record vs reminder separation。

## 8. B6

- effective_at vs recorded_at；
- relation/binding valid@T；
- ConfigurationResolver；
- sequence gap/boot boundary/late arrival；
- History API max window/max points；
- Timeline reference integrity。

## 9. B7

- semantic node mapping；
- selection/focus/isolate/xray；
- LiDAR FOV/Camera frustum；
- pose_source label；
- GLB invalid unit/slot rejected；
- WebGL fallback；
- 20 route cycles no duplicate canvas/memory growth。

## 10. B8

- Time Conductor URL state；
- 30d history；
- GAP no interpolation；
- Replay LIVE isolation；
- configuration@T changes inspector；
- Incident T-30/T+10；
- alarm jump focuses relevant semantic slot。

## 11. B9

- project planned equipment 不自动进入 asset；
- blocking punch 阻止 handover；
- Handover 生成 AS-BUILT；
- Import five-step；
- duplicate import idempotency；
- export scope；
- document metadata security。

## 12. B10

- full 8 existing CI jobs；
- R2 new tests；
- migration rehearsal；
- backup/restore；
- old app compatibility；
- bundle budgets；
- Three FPS/drawcalls/triangles；
- a11y；
- visual regression；
- REA A1.1 vs R2 comparison；
- browser admin/test acceptance。

## 13. Performance targets

目标是验收预算，不是营销数字；B10 以真实生产 build 重测。

### API

- 普通 list/detail 在测试数据规模下 P95 目标 <300ms；
- History bounded query P95 目标 <500ms；
- 冲突检测典型组合秒级内返回；
- 不允许持续 N+1。

### Frontend

- 首屏不加载 Three 和 heavy history charts；
- click→selection <100ms target；
- Inspector skeleton <100ms target；
- Long Task >50ms 不持续出现。

### Three

Premium 50~60FPS、Balanced≥40、Reduced≥30；DPR≤1.5；draw calls<350；triangles<700k 作为首轮预算。

## 14. 四条最终业务链

1. Intake→Schedule→Conflict→Execution→Acceptance→Handover→Asset；
2. Asset→Location→PLC/LiDAR/Camera/VFD→Operations→Asset360；
3. WireRope/Equipment→Inspection→WorkOrder→Replacement→History；
4. Alarm→Incident→Configuration@T→Telemetry/GAP→Replay→Timeline。

任一链断裂，不得宣称成熟平台完成。

