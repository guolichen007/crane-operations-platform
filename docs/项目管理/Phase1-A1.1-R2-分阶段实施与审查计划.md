# Phase1-A1.1-R2 分阶段实施与审查计划

> 每批固定流程：CC 本地开发/测试 → 本地 commit → STOP → OWNER 手动 push feature → GitHub CI → GPT 负责人独立审查 → `BATCH_REVIEW=PASS` 才能进入下一批。

## G0-R1 — 文档/治理封板

### 目标

完整主蓝图、能力地图、技术来源、环境/服务器职责、Batch Gate。

### 允许

Docs、AGENTS 阶段说明、本地会话上下文（不提交）。

### 禁止

SQL/contracts/Go/Vue/Three 产品代码。

### Gate

无结构性 P0/P1；远程 main 不变；feature 文档可审查；会话上下文不提交。

---

## B1 — Data / Contract Foundation

### 技术

PostgreSQL、Go、OpenAPI/contracts codegen、JSON Schema、Capability resolver。

### 交付

- 00005 asset/location/equipment foundation；
- 00006 runtime/history foundation；
- Asset/Equipment/Location contracts；
- Equipment type/spec schema；
- Ownership 三分；
- Capability framework；
- telemetry time-quality persistence；
- 必要 revision/timeline anchor。

### 禁止

项目排期大业务、Wire Rope、PM、Three、完整前端。

### 测试

fresh migration、00004→B1、old app additive compatibility、Inventory non-overwrite、schema validation、legacy telemetry compatibility、RBAC counterproof。

### STOP

OWNER push → GPT 专门审数据库/契约/安全。B1 没 PASS 不准 B2。

---

## B2 — Project Portfolio & Scheduling

### 技术

Go deterministic conflict engine、PostgreSQL tstzrange/GiST、transaction/advisory lock、ECharts design（前端实现可留 B4）、Portfolio Scope。

### 交付

Intake、Lifecycle、Milestone/Activity、Resources、Calendar、Reservation、Dependency、Conflict/Override、Risk/Issue/Change、Baseline。

### Gate

并发 approval 反证、timezone/calendar、blocking conflict、override audit、Portfolio user 无 ProjectGrant 读不到设备/遥测。

---

## B3 — Asset / Location / Equipment Backend

### 技术

Go REST、PG、version/ETag、Idempotency-Key、Audit、server-side paging/filter/sort。

### 交付

Asset/Location/Equipment/Catalog CRUD；Installation history；Relations；Runtime binding read-only；Supplier basic。

### Gate

409、duplicate idempotency、cross-project denial、retire not delete、N+1 counterproof、1000+/10k synthetic list query benchmark。

---

## B4 — Industrial Shell & Core Frontend

### 技术

Vue3、Pinia、TanStack Table/Virtual、TanStack Query candidate、Reka UI、MapProvider/MapLibre candidate、ECharts core。

### 交付

Global Shell、Portfolio 基础 UI、Asset/Equipment Center、forms/Wizard、Global Search、Attention skeleton、Operations Map、真实 CRUD workflow。

### Gate

Admin create/edit；viewer read only；DirtyGuard；409 UX；URL state；1366×768；slow API；10k table；Three/Charts 不在普通首屏加载。

---

## B5 — Lifecycle & Reliability

### 技术

Go + PG；Wire Rope 四模型；Inspection Templates；PM fixed/floating + TIME/METER/HYBRID；Work Order；Calibration；Software/Firmware；Warranty/Spare/Compliance。

### Gate

钢丝绳替换保留历史；PM due；Work Order state machine；Alarm→WO；credential 不入库；法规/企业记录/提醒清晰区分。

---

## B6 — Temporal History Foundation

### 技术

PostgreSQL temporal intervals、entity_revisions、timeline index、metric catalog、ConfigurationResolver、History API、time quality/gap。

### Gate

effective vs recorded；Equipment/Relation/Binding valid@T；boot/sequence gap；History max points；Configuration@T 不使用今天配置解释过去。

---

## B7 — Semantic Digital Twin

### 技术

Three.js、camera-controls、postprocessing、GSAP、GLTFLoader、Meshopt/KTX2、semantic manifest、RuntimeFrameAdapter；Vue Flow PoC。

### 交付

Asset 360 Twin、Equipment Tree/Inspector、FOV/frustum、Select/Focus/Isolate/X-Ray、Topology、GLB model version、performance diagnostics。

### Gate

FPS/drawcalls/triangles、route dispose、no duplicate canvas、WebGL fallback、pose accuracy labels、3D selection 真实绑定 Equipment。

---

## B8 — History UX / Replay / Incident

### 技术

ECharts core、Contextual Time Conductor、ReplayFrameSource、URL time state。

### 交付

History Explorer、Timeline overlays、Replay controls、Incident T-30/T+10、configuration@T Inspector。

### Gate

GAP 不插值、LIVE 不覆盖 Replay、read-only、30d history、alarm jump focus。

---

## B9 — Handover & Data Governance

### 技术

Commissioning/Acceptance/Punch/Handover、documents metadata、PostgreSQL-backed jobs、XLSX import/export。

### 交付

Project→Asset AS-BUILT；Import Preview/Validate/Conflict/Confirm；Export；Document links；background job status。

### Gate

planned ≠ actual；blocking punch 阻止 handover；Excel 不能创建 REAL runtime identity；export scope；formula/path traversal/security。

---

## B10 — Hardening / Release Candidate

### 技术

Playwright、visual regression、REA compare、bundle/runtime profiling、migration rehearsal、backup restore、production contract。

### Gate

所有四条业务链；8/8 existing CI；R2 E2E/visual/performance/history/replay；migration fresh+00004→R2；old app compatibility；isolated server rehearsal；P0/P1=0。

---

## 每批报告统一字段

```text
PHASE=Phase1-A1.1-R2
BATCH=
BASE_SHA=
HEAD_SHA=
BRANCH=
LOCAL_COMMITS=
SCOPE_DONE=
OUT_OF_SCOPE_UNCHANGED=
LOCAL_TESTS=
NOT_RUN=
OPEN_P0=
OPEN_P1=
OPEN_P2=
MAIN_CHANGED=NO
REMOTE_PUSHED=NO
PRODUCTION_CHANGED=NO
PHASE1_A2_STARTED=NO
WORKTREE_CLEAN=
```

