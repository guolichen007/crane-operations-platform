# Homepage H1-0：Operations Center 高保真布局设计

> 分支 `feature/homepage-operations-center-r1`，基线 H0 `76e1cef`（Run 42158118724 九个必需 Job 全绿）。
> 本文件定义 H1 实施的视觉与交互基准。原型是**静态 HTML**（`prototype/homepage-h1/index.html`），数值为 H0 测量所用匿名示例 fixture 的取值，**不是实现，也不是真实统计**；三维画面是现有 `MapSceneManager` 在 1920×1080 下的实际渲染截图（SwiftShader）。

## 1. 原型截图（可复核）

| 视口 | 三维沙盘 | 轻量二维 |
|---|---|---|
| 1280×720 | `prototype/homepage-h1/shot-1280x720-3d.webp` | `shot-1280x720-2d.webp` |
| 1366×768 | `shot-1366x768-3d.webp` | `shot-1366x768-2d.webp` |
| 1920×1080 | `shot-1920x1080-3d.webp` | `shot-1920x1080-2d.webp` |

复核方法：在 `prototype/homepage-h1/` 目录起静态服务（端口 8099），运行 `measure.mjs <输出目录>`（Playwright Chromium）重新截图并输出 `proto-metrics.json`。

## 2. 地图净可见面积：统一口径

**定义**：`净可见面积 = |stage ∩ viewport| − Σ|overlay_i ∩ stage ∩ viewport|`，再除以视口面积。

- `stage`：空间分布容器（`.stage` / 实现中的 `[data-testid=spatial-stage]`）的 `getBoundingClientRect()`；
- `viewport`：`(0, 0, innerWidth, innerHeight)`，即**首屏**，不含需滚动才可见的部分；
- `overlay_i`：位于 stage 内且遮挡画面的控件——面包屑、地图工具、Scene Dock、合规标注；侧栏面板不在 stage 内，不重复扣除；
- 地图内的省份标签不计为遮挡（它们是地图内容）。

H0 原测量（d97c0a7）用的是「画布 − 左右浮层」且未扣除画布内控件，口径更宽松；H1 起统一用上式，H1 实现的前后对比都按此式重新测量。

**门槛（H1 验收）**：1280×720 ≥ 40 %；1366×768 ≥ 40 %；1440×900、1920×1080、2560×1440 ≥ 35 %（三栏常驻时 Inspector 与 Attention 占用更多宽度属设计意图）。

**原型实测**（`proto-metrics.json`，同一口径）：1280×720 = 47.9 %、1366×768 = 47.8 %、1920×1080 = 37.9 %；最小字号 10 px；三档 `scrollWidth = 视口宽`。这是原型数值，**不代表实现已达标**——实现完成后用同一脚本逻辑在真实页面测量。

## 3. 布局与断点

```text
┌ TopCommandBar 56px（品牌 · 地图聚焦项目/项目范围 · 会话 · 时钟） ───────────────────┐
│ SideRail │ 标题行 44px：标题 · 数据截至 · [轻量视图 | 三维沙盘]                       │
│ 200/56px ├ KPI 行 ~82px：5 张单行，可点击=过滤器（选中态描边 accent-blue）─────────────┤
│          ├ 主区 = clamp(420px, 剩余高度, 980px)                                     │
│          │ ┌Attention┐ ┌──────── Spatial Stage（2D/3D）────────┐ ┌Inspector┐      │
│          │ │ 需要关注 │ │ 面包屑          视角/重置/定位工具 │ │ 上下文   │      │
│          │ │ 按严重度 │ │                                    │ │ 列表/详情│      │
│          │ │          │ │      Scene Dock 行政区|资产|告警     │ │          │      │
│          │ └──────────┘ └────────────────────────────────────┘ └──────────┘      │
│          ├ 最近事件 36px：日期+时间（上海） · 「最近 N 条记录，非未处理数」          │
└──────────┴──────────────────────────────────────────────────────────────────────────┘
```

| 宽度 | SideRail | Attention | Inspector | 说明 |
|---|---|---|---|---|
| ≥ 1920 | 200 | 260 | 320 | 三栏常驻 |
| 1600–1919 | 200 | 232 | 288 | 三栏常驻 |
| 1280–1599 | 200（SideRail 不变） | 64 窄条：圆点 + 数字（`title`/`aria-label` 给全称） | 无选中时移到地图下方；选中项目/资产时以覆盖式抽屉出现（宽 320，`role=dialog`，Esc 关闭），地图右侧工具同步内移 320 px 不被遮挡 | 保证地图 ≥ 40 %（1280/1366） |

> **H1-4 修订**：原设计窄断点上限为 1439。实现后按 §2 口径实测 1440×900 三栏常驻仅 26.4 %，低于 35 % 门槛，故窄断点上限改为 1599（常见笔记本 1440/1536 走窄布局，实测 1440×900 = 48.1 %）。原设计 SideRail 折叠为 56 px 未实施（属壳层改动，超出首页范围）；抽屉打开时地图右侧工具被遮挡的问题在验收中发现并修复（`--drawer-inset`），由 `homepage-h1-acceptance.spec.ts` 的 elementFromPoint 用例守护。

- `main` 允许纵向滚动；主区最小高度 420 px，不为一屏缩小数字。
- 最小字号：正文 12 px、说明 11 px、徽标 10 px；KPI 数字 26–28 px。
- 125 % 浏览器缩放：1280 宽度在 125 % 下等效 1024 CSS px，低于桌面最小断点；此时三栏退化为「地图在上、Attention/Inspector 在下」的单列（`max-width: 1100px`），地图高度 `max(360px, 55vh)`，仍无横向溢出。

## 4. 设计系统（与 Inspector 统一）

- **令牌**：沿用 `design/tokens.css`（`--brand-900`、`--surface-*`、`--accent-blue/cyan`、`--success/warning/critical`、`--text-1/2/3`、`--line-soft/strong`），新增字号令牌 `--fs-xs 11 / --fs-s 12 / --fs-m 13 / --fs-l 16 / --fs-num 28`；不引入 UI 框架。
- **面板**：`surface-900` 底、`line-soft` 1 px 描边、圆角 10；Inspector 与 Attention 同一面板样式，标题行 13 px/600 + 右侧 11 px 说明。
- **交互状态**：hover = `surface-850` + `line-strong`；选中 = `accent-blue` 内描边；焦点 = 2 px `accent-blue` outline（`:focus-visible`）；禁用/未接入 = `text-3`，不可点击、无数字。
- **状态色语义**（与地图一致，禁止复用为装饰）：critical=告警，warning=尚未投运/待处理，success=真实运行，text-3=未知/离线/未接入。

## 5. KPI（最多 5 张，口径）

| KPI | 数值 | 副行 | 点击动作 | 数据来源（H1 不新增接口） |
|---|---|---|---|---|
| Inventory 设备 | `devices.length` | 项目数 · 未配置位置数 | 清除状态过滤，Inspector 显示区域排名 | `GET /api/v1/assets/devices` + `projects`（标注 INVENTORY，与资产台账区分） |
| 真实运行 | REAL 且 RUNNING | REAL 在线数 · 运行数 | `?status=RUNNING&source=REAL` | Inventory + Shadow |
| 告警设备 | status=ALARM 台数 | 最近告警记录 N 条（非未处理数） | `?status=ALARM` | Shadow；记录数来自 `GET /api/v1/alarms`（最近 100 条） |
| 离线 / 状态未知 | status=OFFLINE | 有 Binding 但未连接 | `?status=OFFLINE` | Shadow |
| 尚未投运 | NOT_COMMISSIONED | 无 Runtime Binding；SIM 绑定另计 | `?status=NOT_COMMISSIONED` | Inventory |

demo 模式下第 1 张改为「登记项目 / 演示设备」，所有演示数字带 DEMO 徽标。H0-D1（空省份计数）与 H0-D2（UTC 时间）属 H2，H1 不改其计算，对应 `test.fail` 保留。

## 6. Attention（需要关注）

按严重度排序：告警设备 → 离线/未知 → 尚未投运 → 无空间位置 → 维护工单（未接入）→ 检验到期（未接入）。前四项可点击（与 KPI 共用同一过滤器，写 `?status=`）；后两项显示「未接入」，无数字、不可点击、不出现假记录。数量为 0 的项保留但弱化（`text-3`）。

## 7. 空间分布：二维 / 三维

| | 轻量视图（2D） | 三维沙盘（3D，H1 缺省） |
|---|---|---|
| 渲染 | SVG：`public/geo` 的同一 DataV.GeoAtlas 数据，按当前层级加载（全国 / 省 / 市），D3 Mercator 投影与 `GeoProjection` 一致（含南海附图规则） | 现有 `MapSceneManager`（不改渲染与资源管理），`import()` 动态加载 |
| 交互 | 区域点击 = 下钻；区域列表（键盘可达 button）；项目列表点击 = 聚焦/Inspector | 现有拾取、聚合、Glyph、相机、Scene Dock |
| 状态 | 与 3D 共用 `stores/operations`（Pinia）与 URL；切换模式不丢失 project/region/asset/status | 同左 |
| 失败回退 | — | WebGL 初始化失败 → 自动切换到 2D，并显示「三维不可用，已切换轻量视图」 |
| 加载 | 不加载 Three | `import()` 独立 chunk；选择二维时不加载 |

默认视图（**待审查决定**）：规范与任务书倾向「轻量 2D 默认」。但现有 demo / premium / cloud / integration 共约 60 条浏览器测试以 `/` 或 `/#/overview` 进入后直接依赖三维标签（`.province-summary` ×34）与 `__spatialDiagnostics`。H1 采取：**缺省 `view=3d`**（保证现有三维能力与回归不变），二维可随时切换并写入 `?view=2d`，WebGL 失败自动降级为二维；三维模块仍以 `import()` 拆分为独立 chunk。若审查确认改为 2D 默认，需要把上述测试改为显式 `?view=3d` 入口，作为独立批次，不与 H1 混提。

## 8. URL ↔ Pinia ↔ Scene 状态契约

| 参数 | 含义 | 写入时机 | 无效值处理 |
|---|---|---|---|
| `project` | 地图聚焦项目（不过滤） | 顶栏选择、项目列表点击 | 不在授权项目中 → 删除参数并提示 |
| `region` | 当前行政区 adcode（叶子） | 钻取完成、面包屑返回 | 不存在于 manifest → 回到全国并提示「区域不存在」；存在但无可见项目 → 照常进入并显示空态 |
| `asset` | Inspector 选中设备 | 地图/列表/搜索选择；关闭 Inspector 删除 | 不存在 → 删除参数 |
| `status` | Attention/KPI 过滤 | 点击 KPI / Attention | 非枚举值 → 删除 |
| `view` | `2d` / `3d` | 视图切换 | 其他值 → 默认 |

- 单向数据流：URL → `workspace` 解析 → Pinia；Scene 事件 → Pinia → `router.push/replace`。写 URL 前比较当前值，相等则不写，杜绝循环。
- 钻取用 `push`（可前进/后退），过滤与视图用 `replace`。
- 快速切换：每次目标变更生成 token，旧的异步钻取在每个 `await` 后检查 token，过期即放弃（Scene 侧复用 `MapSceneManager.revision`）。

## 9. 不做（H1）

不新增 operations summary 接口、不修 H0-D1/D2、不改 `MapSceneManager` 渲染实现、不引入新 UI 框架或第二个状态仓库、不制造工单/检验数据、不声称 FPS 或加载时间达标（无 GPU 实测）。
