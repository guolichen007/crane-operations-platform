# Phase1-A1.1-R2 REA 前端优秀平台取证报告

> 目标：不研究"页面好不好看"，而是拆**交互机制**——用户是怎么完成工作的。证据分类严格执行 `REA_OBSERVED / PUBLIC_DOCUMENT / INFERENCE`，禁止把 INFERENCE 当事实。原始 evidence 归 `local-private/research/a1.1-r2/`（gitignore），仓库只放脱敏结论。
> rea-agents 6.8.0 · Node 22.23.4 · DESIGN_ONLY=YES。

## 0. License 核验（决定性，先于一切）

| 目标 | License | 结论 |
|------|---------|------|
| realvirtual-WEB（game4automation） | **AGPL-3.0** | 只学交互/架构，**禁止复制代码进入产品** |
| xeokit-sdk | **AGPL-3.0** | 只学 isolate/xray/select/focus/tree 交互模式，**禁止引入** |
| Vue Flow（bcakmakoglu/vue-flow） | **MIT** | 系统拓扑候选，需 PoC |
| Reka UI（unovue/reka-ui，前身 Radix Vue） | **MIT** | headless 可访问 primitive 候选 |
| Open MCT（nasa/openmct） | **Other（NOASSERTION）** | 只学 Time Conductor/Replay 交互模式，**不可复制代码** |
| TanStack Table / Virtual | MIT | DataGrid 状态 + 虚拟化 |

> 核验依据：GitHub API license 字段（PUBLIC_DOCUMENT）。

## 1. Evidence Schema（后续每个目标强制）

```
TARGET / SCREEN / ROUTE
LAYOUT / NAVIGATION / PRIMARY_ACTION / SECONDARY_ACTION
VIEWPORT_STRUCTURE（LEFT_PANEL/RIGHT_PANEL/BOTTOM_PANEL）
3D_CAMERA / 3D_SELECTION / 3D_HOVER / 3D_FOCUS
LIGHTING / POSTPROCESSING / ANIMATION
ASSET_INSPECTOR / HISTORY_UI / TIME_RANGE
NETWORK / LAZY_LOADING / BUNDLE
OBSERVED / INFERENCE / LIMITATION
PATTERN_TO_ADOPT / PATTERN_TO_REJECT
```

## 2. realvirtual-WEB（3D-HMI 标杆）

- **定位**：Browser-based 3D-HMI，Three.js + TypeScript，GLB/glTF，实时信号、KPI overlay、报警、设备文档，点击部件进入 Machine Information System。
- **PATTERN_TO_ADOPT**：① GLB 元数据/语义节点 → 点击部件 → 右侧设备信息系统（正是我们的 semantic_slot → Equipment Inspector）；② 3D HUD 里放 KPI + 报警 overlay；③ 文档/维护资料与 3D component 关联。
- **PATTERN_TO_REJECT**：AGPL 代码本体；游戏化交互过度。
- **LIMITATION**：交互细节为 PUBLIC_DOCUMENT + INFERENCE（未对私有 demo 做浏览器取证）。

## 3. xeokit-sdk（纯交互学习）

- **定位**：BIM/IFC Viewer，pure WebGL、double precision、点云。交互能力：isolate / xray / select / focus / tree / metadata。
- **PATTERN_TO_ADOPT**：Isolate / Hide / X-Ray / Focus / Tree / Metadata 这套 3D 选择交互的**交互语义**（我们自研到 Three.js）。
- **PATTERN_TO_REJECT**：AGPL 代码；其 BIM/IFC/点云领域专用引擎（我们是行车孪生，不需要）。
- **LIMITATION**：仅学交互，不引入 SDK。

## 4. Open MCT（历史时间轴/回放学习）

- **定位**：NASA mission control 框架，telemetry analysis / operations。
- **核心交互**：**Time Conductor** 统一实时/历史时间窗（Realtime / Fixed historical / Last 1m/15m/30m/1h/1d）；多 panel 叠加 telemetry 趋势与事件。
- **PATTERN_TO_ADOPT**：`LIVE | HISTORY` 模式 + 时间范围（1H/6H/24H/7D/30D/CUSTOM）+ 趋势图叠加事件标记——这正是我们的 History Explorer。
- **PATTERN_TO_REJECT**：Open MCT 框架本体（license Other，且是通用 mission control，非行车域）。
- **LIMITATION**：交互模式为 PUBLIC_DOCUMENT。

## 5. Vue Flow / Reka UI / TanStack（可引入候选）

- **Vue Flow（MIT）**：2D 流程图（zoom/pan/minimap），适合系统拓扑（PLC→VFD→Motor、IPC→Switch→LiDAR→Camera）。**结论：候选，先 PoC**；若自有 SVG 足够则不强制引入（拓扑节点有限，SVG 更可控）。
- **Reka UI（MIT）**：无样式可访问 primitive（Dialog/Popover/Dropdown/Tabs/Tooltip/Command/Focus management/WAI-ARIA/键盘导航）。**结论：仅用于这些 primitive，样式全 U·DAO 自有**，不引入其样式。
- **TanStack Table/Virtual（MIT）**：DataGrid 状态（sort/filter/column）+ 虚拟化大行渲染；官方明确"虚拟化不替代服务端分页"。**结论：引入，配合 server-side pagination**。

## 6. ThingsBoard / Grafana（Dashboard 状态与时间窗）

- **ThingsBoard**：Asset/Device 分离 + Relations（已在资产图谱吸收）；dashboard state/widget/time window/drill-down 交互。
- **Grafana Scenes**：dashboard composition、URL state、时间窗、多 panel。
- **PATTERN_TO_ADOPT**：dashboard 状态进 URL（可分享/可回退）、时间窗全局统一、panel 间联动。
- **PATTERN_TO_REJECT**：其 widget/规则链引擎；我们不引入 Grafana/ThingsBoard 本体。

## 7. Autodesk Tandem / AVEVA / Akila / Konecranes Portal / OpenRemote

| 目标 | 学习点（PATTERN_TO_ADOPT） | 拒绝点 |
|------|--------------------------|--------|
| Autodesk Tandem | 3D + 资产属性 + **数据完整度**（Data Completeness） | 其 SaaS 本体 |
| AVEVA CONNECT | 3D/点云/趋势/状态板**并排工作区** | 其点云引擎 |
| Akila | 3D Twin + Alarm + Asset Maintenance + 左侧模块导航 | 其建筑领域 |
| Konecranes Portal | 资产/检查/维护/通知/风险组织方式 | 其 OEM 云 |
| OpenRemote | Asset Tree + Detail + Location + History + 编辑模式 | 其规则引擎 |

> 上述为 SaaS，公开材料有限，多数为 PUBLIC_DOCUMENT/INFERENCE；不绕过登录、不破解、不保存 session。我们的落点已在下述工作台规范固化，不再逐屏模仿。

## 8. 关键结论（本轮定性）

1. **3D 是操作中心，不是展示中心**：点击 Crane→Asset 360→实时/历史/3D/设备/告警/工单，而不是 3D 地图→项目→设备列表→搜索→详情。
2. **LIVE/HISTORY 是 Demo 与成熟平台的分界**：统一 Time Conductor + 事件叠加 + 3D Replay。
3. **headless 优先**：TanStack Table/Virtual + Reka UI 补交互原语，样式全 U·DAO；不引入大 UI Framework、不迁 React/TresJS。
4. **license 红线**：AGPL（realvirtual/xeokit）只学交互；Open MCT（Other）只学模式；MIT（Vue Flow/Reka/TanStack）可引入。
