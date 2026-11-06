# Homepage H0：原始首页审查与 Operations Center 布局设计

> 分支 `feature/homepage-operations-center-r1`，基线 `d97c0a7ea879929d543d734baa90ac4245f0f570`。H0 只交付审查、反证和设计，**不改动首页实现**。H1 需在本设计审查通过后开始。

## 1. 证据与测量方法

| 证据 | 生成方式 | 位置 |
|---|---|---|
| 5 档分辨率截图（API 模式） | Playwright Chromium Headless Shell 156 + 模拟 API（与 premium 测试同一 fixture），`/#/overview`，等待 34 省标签与加载完成后 2.5 s | `evidence/homepage-h0/api-baseline-{1280x720…2560x1440}.webp` |
| 布局测量 | 同一次运行中 `getBoundingClientRect` / `elementFromPoint` / 计算字号 | `evidence/homepage-h0/layout-metrics-d97c0a7.json` |
| REA 交互证据（demo 模式） | rea-agents 6.8.0 `capture-browser-scenario` | `evidence/homepage-h0/rea-*.webp`、`rea-evidence-index.json` |
| 缺陷复现测试 | `tests/e2e/premium/homepage-h0-defects.spec.ts`（`test.fail` 标记，见 §3） | 本提交 |

说明：任务书要求「复用 d97c0a7 的 CI 原始截图」。CI 证据包（`premium-ui-visual-d97c0a7…`、`b4-business-evidence-d97c0a7…`）下载需要仓库认证，只读 API 返回 401，**未能复用**；上表截图是在同一 SHA 上本地重新生成的，渲染器同为 SwiftShader。CI 的 premium 截图只有 1366/1920 两档、不含 1280/1440/2560。

## 2. 五档分辨率布局复核（API 模式，d97c0a7）

| 指标 | 1280×720 | 1366×768 | 1440×900 | 1920×1080 | 2560×1440 |
|---|---|---|---|---|---|
| 标题区 + Cloud Runtime 横幅高度 | 74 + 116 | 74 + 116 | 90 + 116 | 105 + 116 | 105 + 116 |
| KPI 卡数 / 行数 | 8 / 2 | 8 / 2 | 8 / 2 | 8 / 2 | 8 / 2 |
| 地图起点 y | 430 | 430 | 471 | 512 | 512 |
| 地图画布高 | 224 | 272 | 352 | 483 | 843 |
| 地图净可见面积（扣左右浮层）占视口 | **13.7 %** | **16.9 %** | 19.7 % | 27.3 % | 41.4 % |
| 横向溢出 | 无 | 无 | 无 | 无 | 无 |
| 最小字号 | 7 px | 7 px | 7 px | 7 px | 7 px |
| < 10 px 文本节点 | 116 | 116 | 116 | 82 | 82 |
| 行政区按钮可点击 | 被遮挡 | 被遮挡 | 被遮挡 | 被遮挡 | 正常 |
| Scene Dock / 视角切换 / 地图工具 / 面包屑 / 顶栏项目选择 | 正常 | 正常 | 正常 | 正常 | 正常 |

结论：

- **主视觉面积不足**：在最常见的笔记本分辨率（1280/1366）下，地图只占视口 14–17 %，低于标题 + 横幅 + KPI 的面积（约 51 %）。根因是 Cloud Runtime 说明横幅（116 px 常驻说明文字）、8 张两行 KPI、以及「一屏塞下」的固定高度布局。
- **KPI 两行错位**：第二行只有「离线」「尚未投运」两张，左对齐挂在第一张下方，看起来像第一张卡的子项（截图 1920）。
- **左栏内部重叠**：行政区导航、数据筛选、设备运行状态在 1280–1920 下互相覆盖；1280 截图中「设备运行状态」标题与资产概况的数字重叠。
- **可读性**：DEMO/SIM 徽标 7 px，状态说明 8–9 px，在 1280 下不可读。
- **无横向溢出**：五档都满足，控件可见性除行政区按钮外正常。

## 3. 统计口径核对

| 首页位置 | 当前计算 | 后端来源 | 问题 | 处理 |
|---|---|---|---|---|
| KPI「项目 N 项 · 覆盖 X 个省级区域」 | `realProjects.length`；`new Set(province).size` | `GET /api/v1/projects`（Inventory，按项目授权过滤） | 未配置省份的项目 `province=""` 被计为 1 个省（示例 9 个项目全部未配置 → 显示「覆盖 1 个省级区域」） | 复现测试 **H0-D1** |
| KPI「资产 N 台 · 登记行车 M 台」 | `devices.length`；`Σ project.assetDeviceCount` | `GET /api/v1/assets/devices`（Inventory AssetDevice，**不是**资产台账 `GET /api/v1/assets` 的 AssetMasterData） | 首页「资产」与侧栏「资产台账」是两个数据域，数字可能不同而无说明 | H2 设计：改称「Inventory 设备」或接入统一口径，需契约（§6） |
| 「真实现场运行」 | `runtimeSource==='REAL' && status==='RUNNING'` | Inventory + Shadow | 口径正确（REAL 与 SIM 隔离） | 保留 |
| 「模拟运行 SIM」 | `runtimeSource==='SIMULATION'`（**不看连接与运行状态**） | Inventory | 名为「运行」，实际统计的是「运行源为模拟」的绑定数；离线的 SIM 也计入 | H2：改为「模拟绑定 N（在线 M）」 |
| 「待机 / 离线 / 尚未投运」 | 按 `apiAssetStatus` 优先级 | Shadow | 口径与冻结规则一致 | 保留 |
| KPI「告警 N 项」 | 设备 `status==='ALARM'` 的台数 | Shadow `alarmState=ACTIVE` | 单位写「项」实为「台」；同屏 Cloud Runtime 写「当前告警记录 1 条」（`GET /api/v1/alarms` 最近 100 条记录，**不是**未处理告警）。示例中两者 0 与 1 并列，无解释 | H2：区分「告警设备（台）」与「最近告警记录（条）」，后者不得称未处理 |
| demo「模拟告警 5 项」 vs 告警中心「活动演示告警 11」 | 首页数 ALARM 设备；告警中心数 ALARM+OFFLINE+MAINTENANCE 设备生成的演示告警 | 本地常量 | 同一 demo 数据两处数字不同，用户无法对上（REA P1） | H2：统一或注明 |
| 底部「全国事件流」时间 | `sentAt.slice(11,19)` | `GET /api/v1/alarms` | 显示 UTC，顶栏时钟为上海时间；无日期 | 复现测试 **H0-D2** |
| 左栏「全国资产概况」 | `aggregate(visibleProjects)` | 同上 | 钻取到省/市/县后仍为全国口径且标题不变（REA P2） | H1/H2 设计（空间范围派生） |
| 顶栏「地图聚焦项目」 | 仅聚焦相机，不过滤数据 | — | 与 KPI 无关联：选择项目后 KPI 不变 | 设计：聚焦时 Inspector 显示该项目，KPI 不变但标注「全部授权项目」 |

**复现测试**（`tests/e2e/premium/homepage-h0-defects.spec.ts`，真实浏览器 + 模拟 API）：

| ID | 断言 | 当前结果（去掉 `test.fail` 后实测） |
|---|---|---|
| H0-D1 | 全部项目无省份时不得显示「覆盖 1 个省级区域」 | 失败：页面显示「覆盖 1 个省级区域」 |
| H0-D2 | 告警时间显示上海时区 16:49:15 | 失败：显示 08:49:15 |
| H0-D3 | 钻取后 URL 含 `?region=` | 失败：URL 仍为 `#/overview` |
| H0-D4 | 1366 下行政区按钮未被遮挡 | 失败：命中元素为 `.source-filter` |

四条均以 `test.fail(true, 原因)` 提交：缺陷存在时 CI 通过；修复后测试变红，强制删除标记成为常规守卫。这不是跳过，也没有放宽断言。

## 4. 现有能力盘点（复用，禁止重复实现）

| 能力 | 现有实现 | H1 处理 |
|---|---|---|
| 三维地图、钻取、相机、拾取、聚合、Glyph | `three/map/MapSceneManager.ts`（1277 行）、`ScreenSpacePicker`、`CameraRig`、`markers/`、`labels/` | 原样复用，只改挂载容器与尺寸 |
| 帧调度 / 画质 / 性能采样 | `RenderScheduler`、`QualityManager`、`PerfRecorder`（`window.__perfRecorder`） | 复用；H3 用于 GPU 测试 |
| 数据层 | `scene/provider.ts`（demo/api 双实现）、`stores/operations.ts` | 扩展：空间范围、时间戳、口径字段；不另建数据层 |
| 项目 / 设备 Inspector | `ProjectPanel.vue`、`DevicePanel.vue`、`RightInspector.vue` | 复用为右栏内容 |
| 运行统计 | `CloudRuntime.vue`、`cloud/format.ts`（`summarizeDevices`、`formatCloudTime`） | 横幅改为 KPI 内的「口径说明」弹层，统计函数复用 |
| 指标卡 / 迷你趋势 / 状态 | `MetricCard.vue`、`MiniTrend.vue`、`StatusPill.vue`、`SeverityBadge.vue` | 复用 |
| 全局搜索 | `GlobalSearch.vue`（已写 `?asset=` / `?project=`） | 保持 |
| 告警滚动 | `AlarmTicker.vue`、`CloudAlarms.vue` | 底部事件条复用其一，避免第三个实现 |

## 5. Operations Center 布局设计（供审查）

```text
┌ TopCommandBar（品牌 · 项目范围/地图聚焦 · 搜索 · 会话） ───────────────────────────┐
│ SideRail │ 标题行：全国设备运营态势 · 数据截至 hh:mm:ss · [轻量视图 | 三维沙盘]      │
│          ├───────────────────────────────────────────────────────────────────────┤
│          │ KPI × 5（单行，可点击=过滤器）：                                        │
│          │ 资产(Inventory) │ 真实在线/运行 │ 告警设备 │ 离线/未知 │ 尚未投运        │
│          ├──────────────┬───────────────────────────────────────┬────────────────┤
│          │ Attention     │ 空间分布（主视觉，≥45% 视口 @1280）   │ Inspector      │
│          │ · 告警设备    │  默认：轻量视图（省级色块+计数，2D）  │ 全国：区域排名 │
│          │ · 离线/未知   │  切换：Three 数字沙盘（动态加载）     │ 省/市：项目列表│
│          │ · 未投运      │  Scene Dock / 面包屑 / 工具浮于地图内 │ 项目：Project  │
│          │ · 维护/检验   │                                       │ 设备：Device   │
│          │   (未接入)    │                                       │                │
│          ├──────────────┴───────────────────────────────────────┴────────────────┤
│          │ 最近事件（日期+时间，上海时区；「最近 N 条记录，非未处理数」）           │
└──────────┴───────────────────────────────────────────────────────────────────────┘
```

断点规则：

| 宽度 | 左栏 Attention | 右栏 Inspector | 地图 | KPI |
|---|---|---|---|---|
| ≥ 1920 | 260 px 常驻 | 320 px 常驻 | 余量，`clamp(560px, 64vh, 960px)` | 5 张单行 |
| 1440–1919 | 240 px 常驻 | 300 px 常驻 | `clamp(480px, 60vh, 820px)` | 5 张单行 |
| 1280–1439 | 可收起抽屉（默认展开为窄条，仅图标+计数） | 选中时覆盖式抽屉 | `clamp(420px, 62vh, 720px)`，宽度占满 | 5 张单行（紧凑） |

要求与约束：

- 允许 `main` 纵向滚动；最小字号 11 px，徽标 10 px；不缩小数字换取一屏。
- KPI 不超过 5 张，删除与 Cloud Runtime 重复的卡；Cloud Runtime 说明改为 KPI 的口径弹层。
- 默认「轻量视图」不加载 Three（规范 §4：Three 为 opt-in）；用户选择「三维沙盘」后动态 `import()` `MapSceneManager`，偏好写 URL `?view=3d`。轻量视图同时作为 WebGL 初始化失败的回退，显示省级计数与可点击列表，不出现空地图。
- 状态三向同步：`?project=`（聚焦）、`?region=`（空间范围）、`?status=`（Attention 过滤）、`?asset=`（Inspector）、`?view=`；Pinia 为单一来源，场景回调写 Pinia，Pinia 写 URL，URL 变化驱动场景。
- 维护工单、检验到期没有后端数据：Attention 中显示「未接入」行，不可点击，不出现数字。
- 地图合规：保留「仅限内部原型，未经审图」标注，轻量视图同样标注。

## 6. H2 需要的后端契约（先提交契约，未实现）

当前首页需要并行请求 4 个接口并在前端拼接，且 `/api/v1/alarms` 只返回最近 100 条。建议新增只读接口（草案，待审查）：

`GET /api/v1/operations/summary?project=&region=` → `{ observedAt, scope:{projects[], region}, assets:{inventory, real:{bound,online,running}, simulation:{bound,online}, test, notCommissioned, offline, unknown}, alarms:{activeDevices, recentRecords, recentWindow}, provinces:[{adcode, assets, alarmDevices}] }`

权限矩阵：与 `GET /api/v1/projects` 相同的项目授权过滤，在 SQL 中先按授权项目过滤再聚合；未授权项目不得出现在任何计数中；viewer/operator/maintainer/platform_admin 均可读，无写操作。需真实 PG 反证：跨项目用户计数不含他人项目。

## 7. 风险

- 轻量 2D 视图是新组件，必须与 Three 视图共享 `stores/operations` 与拾取语义，否则会出现两套交互。
- `MapSceneManager` 动态加载后首次初始化约数百毫秒（SwiftShader 下 3–5 s），需有骨架与进度。
- 统一口径会改变现有 premium/integration 测试中的文字断言（如「覆盖 N 个省级区域」），需随 H2 同步更新且保留数量断言。
- CI 证据包需认证才能下载，截图对比基线只能在 CI 内完成或由 OWNER 下载。

## 8. 下一批（H1）范围建议

1. 拆分 `Overview.vue`（754 行）为 `OperationsHeader`、`KpiStrip`、`AttentionPanel`、`SpatialStage`（内含 2D/3D 切换）、`InspectorPanel`、`EventStrip`，不改 `MapSceneManager`。
2. 实现 §5 布局与断点；移除 Cloud Runtime 横幅（内容进口径弹层）。
3. 解除 H0-D4（布局遮挡）与 H0-D3（`?region=` 写回）的 `test.fail`。
4. 5 档分辨率截图与布局测量作为 H1 前后对比。
5. H0-D1、H0-D2 与统计口径重命名留在 H2，与后端契约一起审查。
