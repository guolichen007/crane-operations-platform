# Homepage H1-R1 验收报告：首页最终收口

> 分支 `feature/homepage-operations-center-r1`，基线 `9b34de1`（CI `42198617711` 九个 Job 全绿，远端 SHA 与本地一致，开工前工作区仅有不提交的本地文件）。
> 范围：只修数据语义与交互一致性；H1 主体保留，未重写。未改 main，未部署，未混入 G3-D4，未新增后端接口，未开始 H2。
> 环境：以下浏览器结果均在**本地** chrome-headless-shell 1248 + **SwiftShader 软件渲染** + mock API 下取得，不是 GPU 实机，也不是真实 Keycloak/PostgreSQL。同 SHA 九个 CI Job 的结果以 OWNER 推送后的 GitHub Actions 为准，本报告不预先声称。

## 0. 结论速览

| 项 | 结果 |
|---|---|
| R1-1 故障不显示假 0 | 完成。五态数据状态；首次失败显示「不可用」，更新失败保留旧值并标「已过期」 |
| R1-2 统计口径统一 | 完成。契约见 `Homepage-H1-R1-统计范围契约.md`；KPI = 授权 ∩ 来源 ∩ 已显示行政区，状态过滤不改 KPI |
| R1-3 时间语义 | 完成。「数据截至」改为「最近拉取 · 来源 Inventory API」；Cloud Runtime 标注独立轮询并移到页尾 |
| R1-4 地图与详情联动 | 完成。复用 `setInspectorWidth()`；Esc 两段式，2D/3D 同规则 |
| R1-5 回退与恢复 | 完成。context lost 3 s 未恢复则切轻量视图；地图资源失败可重试/切轻量视图。默认视图**未改**，给出数据供决策 |
| R1-6 视觉门槛 | 1280×720 三维 40.2 % → **48.0 %**，门槛恢复为 45 %（不是只改阈值，见 §6） |
| 新增测试反证 | 14 组变异全部按预期失败（§8） |
| 原有回归 | 全部通过（§9）；H0-D1/D2 仍为 expected-fail；H0-D3/D4 为必过 |

## 1. R1-1 API 故障不允许显示假 0

**问题根因**：`Overview.vue` 的 `metrics` 只判断 `loading && !devices.length`，首次加载失败后 `loading=false`、devices 为空，于是用空数组算出全 0；Attention 同理。另外三维舞台在 `onMounted` 里再次调用 `ensureLoaded()`，会在用户无感知时重试失败的首次请求（掩盖失败）；而 `?view=2d` 时三维舞台不挂载，首页数据**根本不会被加载**，一直停在 LOADING（本轮测试发现）。

**修改**：

- `scene/home-metrics.ts`：新增 `homeDataState()`（`loading / unavailable / ready / refreshing / stale`）与 `hasNumbers()`；`homeKpis()` 在无成功数据时所有值为 `null`。
- `scene/provider.ts`：`loadedAt` 更名为 `fetchedAt` 并写明语义；失败时保留旧 projects/devices，只设置 `error`。
- `views/Overview.vue`：数据加载改由页面统一负责（一个 owner）；状态写入 store `dataState`；KPI/Attention/事件条都按状态渲染；错误条区分「暂无可用数据」与「以下为上次成功拉取的结果（已过期）」；页头新增手动刷新按钮。
- `KpiStrip.vue` / `AttentionPanel.vue` / `EventStrip.vue`：`unavailable` 显示「不可用」，`loading` 显示「—」，`stale` 在每张卡上显示「已过期」角标并降低数字不透明度。
- `SpatialStage3D.vue`：删除重复的 `ensureLoaded()`。

**测试**（`homepage-h1-r1.spec.ts › R1-1`）：首次 `/api/v1/projects` 返回 503 并延迟 1.2 s：断言 LOADING 时 KPI 与 Attention 为「—」、页头 `data-state=loading`；失败后五张 KPI 均为「不可用」且**不含任何数字**、四个 Attention 计数为「不可用」、不出现「没有项目」空态；点重试后 `data-state=ready`、KPI 为 3 台/告警 1 台。成功拉取后的合法计数可以为 0（如「真实运行 0 台」）；成功返回空列表时全部为 0 并显示空态。只有未知或失败才显示「—」/「不可用」。第二次拉取失败时旧值 3 台保留、卡片与页头显示「已过期」、页头时间为上次**成功**时间；第三次成功后过期标记全部消失。

## 2. R1-2 统计口径统一

契约文件：`docs/04-前端与可视化/Homepage-H1-R1-统计范围契约.md`（五个维度、推导规则、两个显式例外、KPI 点击行为）。

**问题根因**：KPI 用 provider 全量数据计算（不随来源与区域变化），Inspector 用 `visibleDevices`（随来源）再按 `regionTarget`（目标层级而非已显示层级）过滤并 `.slice(0, 60)` 后取 `length` 作为总数——三处口径不同，会出现「告警 KPI 4、列表 1」之类无解释冲突，60 台以上总数被截断。

**修改**：

- `stores/operations.ts`：新增 `scope`（来源 + **已显示**行政区）、`scopedProjects`、`scopedDevices`、`scopedVisibleDevices`、`scopeName`；KPI、Attention、Inspector 都从这里取数。
- `Overview.vue`：KPI/Attention 基础集改为 `scopedProjects/scopedDevices`；页头增加「统计范围 <来源> · <区域>」标签。Demo「登记行车」也按范围计算。
- `InspectorPanel.vue`：标题总数 = `scopedVisibleDevices.length`（不截断）；只把渲染行限制在 60，并显示「列表显示前 60 台，共 N 台」。
- Inventory KPI（API）：点击进入 `/devices?region=<当前区域>`（Inventory 设备清单），不再是「清除过滤」；`InventoryDevices.vue` 读取 `?region=` 并按授权行政区清单过滤，显示「行政区 xx」。
- 告警卡副文（最近告警记录）不随区域变化，通过卡片 `title` 显式说明，不静默混用。

**测试**（五条要求逐一覆盖，数值全部由夹具推导，没有硬编码到实现中）：

1. Demo 筛选「匿名登记项目」：告警 KPI 变小、Attention 告警数 = KPI、点告警后 Inspector 总数与行数 = KPI；状态过滤后「设备总数」KPI 不变。
2. 浙江 1 台告警、广东 2 台告警：全国 3 台；`?region=330000` 后标签为「浙江省」、告警 KPI 1 台、Attention 1、设备 1 台、Inspector 总数 1 且标题含「浙江省」；返回全国后 3 台。
3. Demo 三维：全国 → 浙江 → 来源「匿名登记项目」→ 来源「演示设备」→ 返回全国，每一步标签与数值同步（浙江只有登记项目，演示来源得到有标签的 0）。
4. 75 台告警设备：KPI 75、Inspector `data-total=75`、标题「75 台」、渲染 60 行、提示「共 75 台」。
5. Inventory KPI：全国点击 → `/devices` 显示 3 台；浙江点击 → `/devices?region=330000` 显示 2 台并标「浙江省」，与卡片数字一致。

单元测试（`home-state.test.ts`）：`inRegion` 前缀匹配、`scopeProjects` 来源∩区域、无位置项目只计入全国、一省一台告警的范围计算、`scopeLabel` 文案。

## 3. R1-3 数据时间语义

- 页头：`最近拉取 YYYY-MM-DD HH:mm:ss · 来源 Inventory API`；`title` 说明「客户端最近一次成功拉取…不是设备遥测的观测时间」。失败时显示「拉取失败 · 暂无可用数据」或「最近成功拉取 … · 更新失败，数据已过期」，不再继续显示最新状态。
- Cloud Runtime 摘要：从标题与 KPI 之间移到事件条之后，并在标题旁标注「独立轮询 15 s · 不刷新首页指标」，不让它的独立轮询看起来像是首页 KPI 已刷新。
- H0-D2（告警时间 UTC）**未在本轮修复**，`homepage-h0-defects.spec.ts` 中的 `test.fail` 保留，本地运行仍为 expected fail；H0-D1 的 `it.fails` 同样保留。
- 测试：R1-3 用例断言「最近拉取」格式与来源、不含「数据截至」、title 文案、Cloud Runtime 标注，以及 DOM 顺序（Cloud Runtime 在 KPI 之后）。原 H1「弱网」用例中「数据截至」断言同步改为新语义并加 `data-state` 断言。

## 4. R1-4 地图与详情联动

**修改**：

- 窄断点抽屉打开时，`Overview.vue` 把 `store.inspectorInset=320` 传给三维舞台，三维舞台调用既有的 `MapSceneManager.setInspectorWidth()`（相机投影偏移，**未改相机系统**）；正在钻取/聚焦时等过渡完成后再应用一次，避免用旧偏移取景。
- `CameraRig.resize()`：原来只有画布宽度 > 1000 px 才应用偏移，而 1280×720 下舞台只有 960 px，偏移被静默跳过。改为「扣掉抽屉后仍 ≥480 px」即应用。仅影响传入非零宽度的调用方（首页抽屉、旧 InventoryOverview）；`CraneTwinScene.frame()` 均以 0 调用，不受影响。
- 二维：选中项目/设备所在的当前层级区域加高亮（`path.selected`）；抽屉打开时 SVG 右侧内缩，地图不会画到抽屉下。
- Esc 统一（2D 与 3D 同规则）：第一次关闭项目/资产详情，三维相机回到**当前行政区层级**（`back(stack.length-1)`），不再直接重置全国；第二次上退一级；只有「返回全国」直接重置。关闭按钮与浏览器后退关闭详情也走同一相机恢复。
- 测试中发现并修复：深链同时带 `region` 与 `project` 时，URL 同步在聚焦请求尚未被消费时就序列化，导致 `?project=` 被丢弃。`useHomeUrlSync.write()` 现在在有待执行的聚焦请求时推迟写入。

**测试**：

- 1280/1440 三维 `?asset=A-1`：`DEVICE_FOCUS` 后读取诊断里的选中标记屏幕矩形，断言中心在舞台内、在抽屉左边界以左，且距「未遮挡区域」中心 < 60 px；「重置全国视角」「聚焦首个登记项目」用 `elementFromPoint` 验证可点击。
- 1280/1440 二维：高亮区域为 330400 且中心不在抽屉下。
- Esc 顺序（3d 与 2d 各一条）：嘉兴市内选中项目 → Esc 1：详情关闭、URL 无 project、层级仍 330400、`REGION_IDLE`、URL 仍 `region=330400` → Esc 2：层级 330000、URL `region=330000`、统计范围标签「浙江省」→ 深链海宁+项目后点「返回全国」直接到 100000 且 URL 无 region。

截图：`evidence/homepage-h1-r1/selected/r1-select-{1280x720,1440x900}-{3d,2d}.webp`。

## 5. R1-5 回退与性能边界

**修改**：

- `MapSceneManager.init()` 返回是否成功、可重复调用；新增 `canvas` 与 `redraw()` 访问器（不改渲染管线）。
- `SpatialStage3D.vue`：监听 `webglcontextlost/restored`；丢失时显示「三维渲染上下文已丢失，正在等待恢复…」并提供「切换轻量视图」；3 s 内恢复则继续三维并请求重绘，否则写入 `webglUnavailable`，页面按 H1 既有逻辑自动切到轻量视图并禁用三维按钮。地图资源（如 `geo/china/100000.json`）加载失败时显示「地图资源加载失败」+「重试」「切换轻量视图」，不再永久空白。

**测试**：`WEBGL_lose_context` 先丢失后恢复 → 保持三维、无回退提示；再丢失不恢复 → 自动二维、二维地图可见、三维按钮禁用、告警 KPI 点击后 Inspector 总数 1。资源首次 500 → 错误条 → 重试成功 34 个省级标签；持续 500 → 切换轻量视图后二维地图可见。

**默认 2D/3D 推荐数据**（`vite build` + `vite preview`，1440×900，各 5 次，脚本 `evidence/homepage-h1-r1/measure-first-content.mjs`，原始数据 `metrics-h1-r1.json`）：

| 指标 | 轻量视图（2D） | 三维沙盘（3D） |
|---|---|---|
| KPI 数字出现（中位数） | 201 ms | 216 ms |
| 主地图可交互（中位数，min–max） | **210 ms**（208–332） | **2021 ms**（1997–2144） |
| 首页 JS 下载（未压缩） | 296 KB | 1347 KB |
| 其中 three + MapSceneManager + SpatialStage3D | 0 | 1039 KB（构建 gzip：three 280 KB + MapSceneManager 26 KB + Stage 5 KB） |
| 行政区边界 GeoJSON | 1072 KB | 1072 KB |
| 初始化失败时 | 不依赖 WebGL | 自动回退二维（H1 用例 + 本轮 context-lost 用例） |

说明与边界：时间是 SwiftShader 软件渲染下的本地数值，三维地图时间主要受软件光栅化影响，**不能当作 GPU 设备的首屏时间**，只可用于 2D/3D 相对比较；本报告不对 FPS/GPU 帧时间作任何达标声明（仍 NOT_RUN）。初始化失败率需要真实终端遥测，本地无法给出比例。视觉与业务价值：三维提供地形层级、聚焦动画与巡演，适合大屏展示；二维首屏更快、点击目标更大、无 GPU 依赖，适合日常运营与低配终端。两者共用同一 store、URL、Esc 规则与统计口径，切换无业务差异。

**推荐（待 OWNER 决定，未修改默认值）**：日常运营入口默认轻量视图、展示场景保留三维入口（或按设备能力/用户偏好记忆）。在产品决策批准前，代码默认仍为三维。

## 6. R1-6 视觉验收门槛（45 % vs 40 %）

**做法**：未改面积口径，也未删减字体、KPI 或地图控件。只调整垂直布局：

1. Cloud Runtime 摘要（独立数据源，见 R1-3）从标题与 KPI 之间移到事件条之后，释放约 43 px；
2. 主网格高度从 `100dvh − 64 − 250` 改为 `100dvh − 226`，舞台填满首屏 KPI 以下区域；
3. 窄断点隐藏标题副文「空间资产 · 协同运营」，为页头新增的「最近拉取」「统计范围」腾出横向空间。

**结果**（同一用例、同一夹具；前值为 `9b34de1` 重新实测）：

| 视口 | 三维 前 → 后 | 二维 前 → 后 | 门槛 | 最小字号 | KPI | 横向溢出 |
|---|---|---|---|---|---|---|
| 1280×720 | 40.2 % → **48.0 %** | 42.4 % → 50.1 % | 45 %（原 40 %） | 10 px | 5 | 0 |
| 1366×768 | 42.2 % → **51.0 %** | 44.1 % → 52.9 % | 45 %（原 40 %） | 10 px | 5 | 0 |
| 1440×900 | 48.1 % → 55.8 % | 49.7 % → 57.3 % | 35 % | 10 px | 5 | 0 |
| 1920×1080 | 37.6 % → 42.1 % | 38.6 % → 43.1 % | 35 % | 10 px | 5 | 0 |
| 2560×1440 | 44.3 % → 44.3 % | 44.9 % → 44.9 % | 35 % | 10 px | 5 | 0 |

`homepage-h1-acceptance.spec.ts` 的 1280/1366 门槛由 40 恢复为 H0 审查的 45；反证：回退网格高度后该用例以 40.2 % 失败（§8 M11）。2560×1440 主网格已到 980 px 上限，未变化。

**取舍**：Cloud Runtime 摘要不再出现在首屏（1280×720 需要向下滚动才能看到）。它是独立轮询的 Cloud Shadow 统计，与首页 KPI 口径不同，放在首屏 KPI 旁边反而容易被误读为同一组指标；如 OWNER 认为首屏必须保留，可改为页头一个紧凑徽标，但需重新评估 45 %。

**选中后 Inspector 打开时的实际可用区域**（新增用例，抽屉覆盖部分不计入）：

| 视口 | 可用地图宽 × 高 | 占视口 |
|---|---|---|
| 1280×720 | 642 × 494 px | 32.2 % |
| 1366×768 | 728 × 542 px | 35.7 % |
| 1440×900 | 802 × 674 px | 40.2 % |

用例断言下限为 ≥600 × ≥400 px（不是面积目标，只防止抽屉吞掉地图）；三维相机已按此区域取景（R1-4）。

截图：五档 `evidence/homepage-h1-r1/after/home-<w>x<h>-{3d,2d}.webp`；前后对照 `before-after-<w>x<h>-{3d,2d}.webp`（左为 `9b34de1` 原证据，右为本轮）；Inspector 打开 `selected/r1-inspector-<w>x<h>.webp`。

## 7. 代码文件与修改原因

| 文件 | 原因 |
|---|---|
| `src/scene/home-metrics.ts` | 数据状态、范围契约纯函数（`homeDataState`/`scopeProjects`/`inRegion`/`scopeLabel`）、KPI 空值规则与提示 |
| `src/scene/provider.ts` | `loadedAt` → `fetchedAt`，注明为客户端拉取时间 |
| `src/stores/operations.ts` | 统计范围（scope/scoped*）、`dataState`、`inspectorInset` |
| `src/views/Overview.vue` | 统一数据加载 owner、数据状态、页头时间/范围/刷新、Inventory KPI 工作流、抽屉 inset、Cloud Runtime 位置 |
| `src/views/overview/KpiStrip.vue` / `AttentionPanel.vue` / `EventStrip.vue` | 不可用/加载/过期渲染 |
| `src/views/overview/InspectorPanel.vue` | 范围口径、完整总数、显式截断提示 |
| `src/views/overview/SpatialStage3D.vue` | Esc 规则、详情关闭相机回当前层级、`setInspectorWidth`、context lost 与资源失败恢复 |
| `src/views/overview/SpatialStage2D.vue` | 选中区域高亮、Esc 注释同步 |
| `src/views/overview/useHomeUrlSync.ts` | 聚焦请求未消费前不序列化（防止丢 `?project=`） |
| `src/views/overview/home.css` | 网格高度、页头状态样式、恢复条、二维高亮与抽屉内缩 |
| `src/views/InventoryDevices.vue` | 支持 `?region=`，与 Inventory KPI 同范围 |
| `src/components/CloudRuntime.vue` | 紧凑模式标注独立轮询 |
| `src/three/map/MapSceneManager.ts` | `init()` 可重试并返回结果、`canvas`/`redraw()`、诊断输出 `focusedScreenRect` |
| `src/three/core/CameraRig.ts` | 视图偏移启用条件（见 §4） |
| `tests/home-state.test.ts` | 数据状态与范围契约单测 |
| `tests/e2e/premium/homepage-h1-r1.spec.ts` | 新增 20 条浏览器用例 |
| `tests/e2e/premium/homepage-h1-acceptance.spec.ts` | 1280/1366 门槛 45 %；「数据截至」断言改为新语义 |
| `.github/workflows/ci.yml` | 视觉证据产物增加 `inspector-usable-*.json`（仅 artifact 路径） |

## 8. 反证（撤销修复 → 对应测试失败 → 恢复后通过）

每组用脚本把一处修复替换回旧逻辑，单独运行对应用例，然后还原文件（还原后全部重跑通过，§9）。

| # | 撤销内容 | 用例 | 失败断言 |
|---|---|---|---|
| M1 | KPI 不判断数据状态，直接计算 | R1-1 首次加载 | 期望 `—`，实际出现计算结果 |
| M1b | 首次失败视为 ready | R1-1 首次加载 | 期望 `unavailable`，实际 `ready` |
| M2 | 更新失败不标过期 | R1-1 更新失败 | 期望 `stale`，实际 `ready` |
| M3 | KPI 基础集用全量设备 | R1-2 单省告警 | 期望 `1台`，实际 `3台` |
| M4 | Inspector 总数取截断后长度 | R1-2 超过 60 | 期望 `75`，实际 `60` |
| M5 | Inventory KPI 仍为清除过滤 | R1-2 Inventory KPI | 期望 URL `#/devices`，实际停在首页 |
| M6 | 页头恢复「数据截至」 | R1-3 | 实际「数据截至 …」不匹配「最近拉取 … · 来源 Inventory API」 |
| M7 | Esc 在聚焦预设下重置全国 | R1-4 Esc（3d） | 期望 `REGION_IDLE`，实际 `COUNTRY_IDLE` |
| M8 | 不调用 `setInspectorWidth` | R1-4 三维选中（1280、1440） | 标记中心偏离未遮挡区域中心 152 / 149 px（阈值 60） |
| M9 | context lost 不回退 | R1-5 上下文丢失 | 未出现回退提示 |
| M10 | 资源失败不显示恢复条 | R1-5 资源失败 | 恢复条不存在 |
| M11 | 网格高度回到 H1 | H1 布局 1280×720 | 期望 ≥45，实际 40.2 |
| M12 | 去掉二维选中高亮 | R1-4 二维选中 | 期望 1 个高亮区域，实际 0 |
| M13 | URL 同步不等待聚焦请求 | R1-4 Esc（2d） | `?region=…&project=` 深链后 project 面板不存在 |

## 9. 回归结果（本地，最终代码）

| 套件 | 结果 |
|---|---|
| typecheck / lint / build | 通过 |
| vitest | 21 文件，176 passed + 1 expected fail（H0-D1 `it.fails` 保留） |
| premium `homepage-h1-r1.spec.ts`（新增） | 20 passed |
| premium `homepage-h1-acceptance.spec.ts` | 14 passed（按 bash 约 180 s 上限分批运行） |
| premium `homepage-h0-defects.spec.ts` | 10 passed（含 H0-D2 `test.fail` 仍按预期失败；D3/D4 必过） |
| premium `premium.spec.ts` / `command-scope` / `asset-guards` | 21 passed，1 skipped（既有真实 GPU 用例） |
| demo（7 个 spec，分批） | 36 passed（含「单机视图…ESC 返回全国」：该用例 Esc 时设备选中且三维在 DEVICE_FOCUS，未钻取行政区，因此「当前层级」即全国，与新规则一致） |
| cloud（3 个 spec） | 14 passed |
| B4 Keycloak + PostgreSQL / PATCH / 409 / RBAC / 安全 | 本地无 Docker，NOT_RUN；本轮未改认证、资产 PATCH、乐观锁、RBAC 与后端代码，以 CI `b4-business-acceptance`、`a1-security`、`security` 为准 |

## 10. 剩余风险

1. **GPU 与真实环境**：FPS、GPU 帧时间、真实终端首屏时间、WebGL 初始化失败率均 NOT_RUN；§5 的时间只能做 2D/3D 相对比较。
2. **告警记录范围**：「最近告警记录」与事件条仍是全部授权范围最近 ≤100 条，不随来源/区域变化（已显式标注）；真实告警记录、活动告警设备、待处理事件的区分属 H2。
3. **Inventory 清单区域过滤**依赖前端用 geo-manifest 推导项目祖先路径；H2 的统一统计接口应在服务端给出同一结果。
4. **context lost 3 s 宽限期**是经验值；真实 GPU 驱动重置可能更慢，届时会先回退二维（用户可再切回三维的入口在回退后被禁用，需刷新页面）。
5. **Cloud Runtime 下移**使 1280×720 首屏不再显示 Cloud 摘要，需 OWNER 确认此取舍（§6）。
6. **默认视图**仍为三维，等待产品决策（§5 推荐）。
7. H0-D1（空省份计数）、H0-D2（告警时间 UTC）未修，保留 expected-fail，属 H2。

## 11. H2 后续规划（仅设计，本轮未实施）

待本轮验收通过后，H2 建议按「契约 → 权限矩阵 → 真实 PostgreSQL 测试 → API → 前端切换」顺序：

1. **统计身份关系**：明确 Inventory Project / AssetMasterData / Inventory AssetDevice 三者的主键与映射，决定首页「设备」统计的唯一身份（避免同一台设备在两套档案中重复计数）。
2. **统一运营统计接口**：服务端按本契约（授权 ∩ 来源 ∩ 行政区；状态只影响列表）返回 KPI、Attention 与分区汇总；前端不再自行计算，避免前后端两套口径。
3. **时间契约**：`observedAt`（设备观测）、`fetchedAt`（客户端拉取）、`generatedAt`（服务端统计生成）分离，并定义过期判定阈值；顺带修复 H0-D2 并取消其 expected-fail。
4. **来源隔离**：REAL / SIMULATION / TEST 在接口层分别计数，不允许合并成「运行中」。
5. **告警语义**：区分告警记录、活动告警设备、待处理事件，分别提供数量与筛选。
6. **刷新链路**：Cloud Runtime 状态变化可靠地驱动首页统计刷新（推送或带版本的轮询），而不是两个独立轮询。
7. **时区**：全部按中国标准时间展示完整日期时间；修复 H0-D1 空省份计数并取消 `it.fails`。

未经确认不开始 H2、不新建后端统计接口、不部署。

## 12. 收口补充（H2-0 任务书第一阶段，提交于 28a7874 之后）

1. **`/devices?region=` 边界**：核对发现 `9b34de1..28a7874` 中 `InventoryDevices.vue` 在行政区清单加载失败或区域不存在时会把设备列表过滤为空并显示「0 台资产」——与 R1-1「失败不显示 0」矛盾。已修正：清单不可用 / 区域不存在显示明确错误、计数为「—」、不展示任何设备；格式错误的参数提示后忽略。区域过滤只在服务端已授权返回的数据上**收窄**，与 `?project=` 取交集，不扩大授权。
2. 新增 2 条浏览器用例（共 22 条）：原清单、`?project=`、`?region=`、两者交集、无授权项目的区域、未知区域、格式错误、首页 KPI → 清单 → 后退/前进；清单不可用。反证：恢复「失败当 0 显示」后两条用例均失败（期望「— 台资产」，实际「0 台资产」）；去掉不可用提示后清单不可用用例失败。
3. **表述更正**：契约 §5 与本报告 §1 原写「只有成功返回空列表才显示 0」，表述错误。正确规则：完整成功拉取后任何指标的合法计数都可以为 0；只有未知或失败才显示不可用。代码行为本来如此，R1-1 首次失败用例补充断言「真实运行 0 台」为合法 0。
