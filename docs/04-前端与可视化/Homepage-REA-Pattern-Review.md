# Homepage REA 模式研究（H0）

> 分支 `feature/homepage-operations-center-r1`，基线 `d97c0a7ea879929d543d734baa90ac4245f0f570`（Run 42147298777，九个必需 Job 全绿）。
> 本文是 H0 研究交付物，只记录证据、推断和适配建议，**不实施首页重构**。证据文件位于 `evidence/homepage-h0/`。

## 0. 工具与边界

| 项 | 实际情况 |
|---|---|
| 官方来源 | GitHub `morluto/rea`（MIT，主页 rea.tools），npm 包名 **`rea-agents`**（不是 npm 上无关的 `rea` 0.0.1） |
| 版本 | 最新发布 `rea-agents-6.8.0`（2026-10-25）；`package.json` `engines.node = ^22.23.4 \|\| ^24.22.1 \|\| >=26.12.1` |
| 安装方式 | 独立工具目录 `/tmp/rea-tools`，`npm install --ignore-scripts rea-agents@6.8.0`；未改项目 Node 版本、`package.json` 或锁文件；未运行 `rea setup`，因此**没有**写入任何代理配置 |
| Node | VM 内 v22.23.4，满足 engines |
| Hopper / IDA / Ghidra | 未安装、未使用（本研究只需浏览器场景能力） |
| Codex MCP 配置 | **未完成**。本环境不是 Codex；`rea setup` 会改写代理配置文件，任务书要求在独立开发工具环境中配置，需在 OWNER 的 Codex 环境执行 `npx rea-agents@6.8.0 setup`（或按 `docs/installation.md#mcp-registry` 手动注册）。本研究用同一包的 CLI（与 MCP 同一契约 `capture_browser_scenario`）取证 |
| 浏览器 | Playwright 自带 Chrome Headless Shell 156.0.8078.4（软件渲染，非 GPU） |
| 目标 | 仅本仓库 `d97c0a7` 的本地 web-console（demo 模式 dev server，无身份认证）。**未分析任何第三方商业平台**；第三方模式沿用已有《B4 数字孪生 REA 模式研究》中基于公开文档的结论，本轮没有新增可观察证据，下文明确标为「既有文献结论」 |
| 版本差异 | 官方文档（main）描述了 `validate-browser-scenario`，6.8.0 CLI 不存在该命令；`browser.args` 不被接受（WebGL 仍可用） |

运行的 REA 场景（Evidence ID 见 `evidence/homepage-h0/rea-evidence-index.json`，其中 `classification` 区分正式与探索性运行）：

**正式运行：5 组，全部步骤完成**（P4 含 1280 与 1366 两次运行，按场景计为第 4 组的两个视口）。另有 **2 次探索性运行失败**，未用于任何结论：`min`（定位器 `.province-summary` 命中 34 个元素，严格模式失败）与 `s1`（第一次因 `browser.args` 不被接受而报输入错误，修正后又因 vite 进程随上一次 bash 调用退出而连接被拒）。H0 初版文档未写明这 2 次失败，此处更正。

| 场景 | 视口 | 步骤 | Evidence |
|---|---|---|---|
| P1 告警模式 → 告警筛选 → 告警中心 | 1920×1080 | 9/9 完成 | `ev_1bfc8989…` |
| P2 全国 → 浙江 → 嘉兴 → 海宁 → 返回全国 | 1920×1080 | 11/11 | `ev_1ccd6f6d…` |
| P3 Ctrl+K 搜索 → 设备 Inspector → 单机监控页 | 1920×1080 | 11/11 | `ev_15300437…` |
| P4 资产模式响应式 | 1280×720 / 1366×768 | 5/5 ×2 | `ev_8fea13f6…` / `ev_e607f979…` |

另用 Playwright（同一浏览器）在 API 模式 + 模拟 API 下测量了 5 个分辨率的布局（`layout-metrics-d97c0a7.json`），见《Homepage-H0-首页审查与布局设计》。

## 1. 模式 A：KPI / 告警 → 资产详情 交互链

- **来源**：本仓库 d97c0a7 首页（demo），REA P1。
- **实际观察**：
  - 点击 Scene Dock「告警」后，地图回到全国、状态过滤置为 ALARM；左栏「模拟运行态」色条只剩告警段（截图 `rea-p1-alarm-filter-1920.webp`）。URL 保持 `#/overview`，**没有**任何查询参数。
  - 顶部「模拟告警 5 项」KPI 卡**不可点击**（只是展示）；从 KPI 到具体告警没有直接路径。
  - 侧栏「告警」进入 `#/alarms`，该页统计「活动演示告警 11」，而首页 KPI 写「模拟告警 5」：两处口径不同（首页按设备状态=ALARM 计数，告警中心按告警记录计数），页面上无解释（`rea-p1-alarm-center-1920.webp`）。
  - 告警中心的行可链接到设备，但回到首页不会定位到该设备。
  - 整个链路网络请求为 0（demo 数据为本地常量）。
- **推断**：当前「告警」只是一种地图配色模式，而不是可行动的注意力入口；KPI→列表→详情→地图 没有闭环，且两套计数口径会造成用户误读。
- **技术原理（参考做法）**：成熟运营台的注意力入口是「KPI = 一个已命名的过滤器」：点击 KPI 等价于写入 URL 查询（如 `?attention=alarm`），列表与地图都从同一过滤器派生；详情返回时恢复过滤器。
- **性能代价**：仅路由与 Pinia 状态，可忽略；地图只需 `setFilter`（已有）。
- **适配方案**：H1 左侧 Attention 列：每条可点击，写 `?status=ALARM` 等查询；KPI 卡全部可点击并带「口径」tooltip；首页「告警」改称「告警设备 N 台」，告警中心为「告警记录 N 条」，两者同屏出现时注明差异。
- **验证方法**：Playwright：点击告警 KPI → URL 含过滤 → 列表条数 = KPI 数 → 点击一行 → Inspector 显示该设备 → 后退恢复过滤。真实 B4 环境另断言 `GET /api/v1/alarms` 被调用。

## 2. 模式 B：空间分布、地图钻取与 Inspector

- **来源**：本仓库首页，REA P2；既有文献结论（DataV 层级树 + 钻取恢复、AntV L7 setActive/setSelect）。
- **实际观察**：
  - 钻取 全国→浙江→嘉兴→海宁 四步全部完成，单步 0.8–2.9 s（含相机动画，SwiftShader）。面包屑正确（`全国 › 浙江省 › 嘉兴市 › 海宁市`），「返回全国」按钮出现。
  - **URL 全程不变**（`#/overview`）：钻取状态不可分享、刷新丢失。代码中 `?region=` 只在加载时读取，从不写回（单向联动，已写复现测试 H0-D3）。
  - 到海宁后左栏「全国资产概况」仍显示全国口径（23 省 / 28 项目 / 54 台），标题也仍是「全国」——统计面板不随空间范围变化（`rea-p2-county-1920.webp`）。
  - 右栏在区县级仍是「近期实施计划」全国列表，没有切换为该区域的项目 / 资产 Inspector。
  - 县级视角下地图被拉近到极大的侧墙几何，主体只占画面中部约 1/4。
- **推断**：地图钻取能力（相机、几何、标签）成熟，但「空间范围」没有成为页面的一等状态：统计、Inspector、URL 都没订阅它。
- **技术原理**：把 `spatialScope = {level, adcode}` 作为与项目范围平级的状态，统计与列表都是 `f(数据, spatialScope, 项目范围, 时间)` 的派生；URL ↔ Pinia ↔ 场景三向同步（场景事件写 Pinia，Pinia 写 URL，URL 变化驱动场景）。
- **性能代价**：派生统计为 O(n)（n≈50），可忽略；需防止 URL 写回与场景回调互相触发（用「来源标记」或比较当前值）。
- **适配方案**：H1/H2 中 `MapSceneManager.change` 回调写入 `?region=`；左栏标题与数字按空间范围派生；右栏 Inspector 在省/市级显示该区域项目列表，项目级显示项目详情（复用 `ProjectPanel`），设备级显示 `DevicePanel`。
- **验证方法**：Playwright：钻取后 URL 含 `region=330481`；刷新后 `data-adcode=330481`；左栏统计等于该区域项目汇总；后退键回到上一级。

## 3. 模式 C：设备监控趋势、状态变化与时间上下文

- **来源**：本仓库，REA P3（Ctrl+K → 设备 → 单机监控页）。
- **实际观察**：
  - Ctrl+K 搜索「南京」→ 选择 → URL 变为 `#/overview?asset=DEMO-001-A`（**此入口会写 URL**，与钻取不同），相机聚焦，右侧出现设备 Inspector（含数字孪生缩略图、负载/今日运行/坐标）（`rea-p3-device-inspector-1920.webp`）。
  - Inspector 的「查看详情」进入 `#/monitor?device=DEMO-001-A`：有负载趋势折线（14:26–14:32）、模拟事件列表、「最后心跳 2026-10-20 10:00:00」（`rea-p3-monitor-1920.webp`）。
  - 首页本身**没有任何时间上下文**：KPI 不显示数据时间或刷新时间；事件条只有时分秒、不带日期；API 模式下事件时间是 UTC 字符串截取（`sentAt.slice(11,19)`），而顶栏时钟是上海时间（复现测试 H0-D2：期望 16:49:15，实际 08:49:15）。
- **推断**：「趋势」与「时间」只存在于单机页；首页缺少「这些数字截止到什么时候」的表达，混合 UTC/本地时间会误导值班判断。
- **技术原理**：首页每个区块显示 `observedAt`（数据时间）与 `fetchedAt`（拉取时间），超过陈旧阈值显示 STALE；时间统一经 `formatCloudTime`（已存在，Asia/Shanghai）。趋势只在 Inspector 中按需加载（避免首页加载 ECharts 全量）。
- **性能代价**：时间格式化可忽略；Inspector 迷你趋势用已有 `MiniTrend.vue`（SVG），不引入 ECharts。
- **适配方案**：H2：provider 事件改用 `formatCloudTime`；底部事件条显示日期+时间与「最近 N 条 / 截止 hh:mm」；KPI 区标注「最近同步 hh:mm:ss」。真实运行趋势需后端时间序列接口，**当前不存在**，H2 以「趋势未接入」明示，不画假曲线。
- **验证方法**：单元测试 UTC→上海转换；Playwright 断言事件时间 = 16:49:15（解除 H0-D2 的 `test.fail`）。

## 4. 模式 D：1280 / 1366 / 1920 响应式与组件行为

- **来源**：REA P4（demo 1280/1366）+ Playwright 布局测量（API 模式 5 分辨率）。
- **实际观察**（数字见 `layout-metrics-d97c0a7.json`）：
  - 1280×720 demo：KPI 6 张单行，地图可见但左栏「数据筛选」与「行政区导航」被裁切（`rea-p4-demo-1280.webp`）。
  - API 模式下 KPI 有 **8 张卡**，在 1280–2560 全部换成两行（`离线`、`尚未投运` 掉到第二行左侧，与第一行错位），再加上 116 px 高的 Cloud Runtime 横幅，地图起点被推到 y=430（1280）/ y=512（1920）。
  - 地图净可见面积（扣除左右浮层）：1280 → **13.7 %**、1366 → 16.9 %、1440 → 19.7 %、1920 → 27.3 %、2560 → 41.4 % 视口。主视觉在常见笔记本分辨率下不是主体。
  - 页面无横向溢出（五档 scrollWidth = 视口宽）；`main` 不滚动，内容靠压缩适配，导致左栏互相覆盖：1280–1920 下行政区按钮被「数据筛选」区块/底部事件条遮挡（复现测试 H0-D4）。
  - 可读性：最小字号 7 px（SIM/DEMO 徽标），小于 10 px 的文本节点 1280–1440 有 116 个、1920+ 有 82 个。
  - 首页顶栏项目选择器在 1280 下可见可用（G0-B4-1 修复已生效）。
- **推断**：布局以「一屏塞下」为目标，代价是主视觉与可读性；应允许合理纵向滚动，把非核心信息（Cloud Runtime 说明、口径说明）收进可展开说明。
- **技术原理**：CSS Grid 三栏（Attention / Map / Inspector），断点只调整栏宽与是否折叠为抽屉；地图高度 `clamp(420px, 62vh, 900px)`；KPI 固定 4–5 张单行；字号下限 11 px（徽标 10 px）。
- **性能代价**：地图尺寸变化会触发 renderer resize（已有 ResizeObserver），需避免 hover 改变栏宽（规范已禁止）。
- **适配方案**：见设计文档 §4 线框；1280/1366 下左右栏变为可收起抽屉，地图至少占视口 45 %。
- **验证方法**：Playwright 在 1280/1366/1440/1920/2560 断言：地图净可见面积占比阈值（1280 ≥ 40 %）、关键控件 elementFromPoint 命中自身、无横向溢出、可见文本最小 10 px。

## 5. 第三方公开 Demo 研究（H1-3 补充）

只在无需登录的公开范围内操作；没有登录、注册、绕过认证或抓取私有代码。所有运行使用同一 rea-agents 6.8.0 `capture-browser-scenario`，Evidence 见 `rea-evidence-index.json` 的 `external_public_demos`。

### 5.1 Grafana：URL 中的时间上下文与变量（已实际观察）

- **来源**：`https://play.grafana.org`（Grafana 官方公开演示站，匿名可读，版本 13.4.0），仪表板 `qD-rVv6Mz`（State timeline）与 `testdata-nested-variables-drilldown`。
- **可复现步骤**（Evidence `ev_68bc3ed5…`、`ev_de82723e…`）：
  1. 打开 `…/d/qD-rVv6Mz/…?orgId=1&from=now-6h&to=now`。
  2. 点击时间选择器 → 选择 "Last 24 hours"。
  3. `goto` 回到 `from=now-6h` 的原始 URL（等价于用户用历史/分享链接回到旧状态）。
  4. 打开嵌套变量仪表板，`goto` 带 `var-datacenter=B&var-server=BB&viewPanel=panel-4` 的深链 → 按 Esc。
  5. `goto` 带不存在的变量值 `var-datacenter=ZZZ-NOT-EXIST`。
- **实际观察**：
  - 加载后 URL 被规范化补全：追加 `timezone=browser`，变量默认值写入 `var-server=$__all&var-pod=$__all`——URL 始终是完整状态的序列化，而不是只记用户改过的项。
  - 选择 Last 24 hours 后 URL 立即变为 `from=now-24h`，且保持**相对时间**表达（不是绝对时间戳），分享出去仍是「最近 24 小时」。
  - 通过 URL 回到 `from=now-6h` 时界面恢复为 Last 6 hours。
  - `viewPanel=panel-4` 深链直接打开单面板视图，面包屑显示 "View panel"，变量 `B / BB` 生效、`pod` 选项按 `server` 级联（BBA…BBH）（`ext-grafana-viewpanel-vars.webp`）。按 Esc 后 URL **只移除** `viewPanel`，其余状态保留。
  - 不存在的变量值 `ZZZ-NOT-EXIST` 被原样接受并显示在选择器中，面板渲染出空结果，没有回退或提示（`ext-grafana-invalid-var.webp`）。
- **局限**：只观察了匿名可读仪表板；未观察编辑、登录后的权限过滤；未检查浏览器原生前进/后退按钮（REA 6.8.0 动作集无 back/forward，用 `goto` 代替），因此「历史栈条目粒度」未直接观察到。
- **对本项目的适配**：
  - 首页 URL 应在加载后被**规范化**：缺省值不必全部写入，但已生效的范围（`project`、`region`）要写回，保证复制的链接可复现当前画面。
  - 浮层/聚焦类状态（Inspector 打开的资产）用独立参数（`asset=`），关闭时只删除该参数——对应 Grafana `viewPanel`。
  - **不照搬** Grafana 对非法变量的「原样接受」：本项目 `region` 未知或无权限时必须回退到全国并给出提示（H1-2 D3 要求），因为空地图会被误读为「该区域无设备」。
  - 时间上下文目前首页没有用户可选时间范围（数据为最近同步快照），本轮不新增时间选择器；仅显示「数据截至」时间（H2）。

### 5.2 ThingsBoard：Dashboard State（NOT_OBSERVED，仅公开文档）

- **来源**：`https://demo.thingsboard.io`（官方 Demo）与 `https://thingsboard.io/docs/user-guide/dashboards/`、`/docs/user-guide/actions/`。
- **实际观察**：打开 Demo 直接重定向到 `/login`（`ext-thingsboard-demo-login.webp`，Evidence `ev_50a71c3f…`）。仪表板需要账号，按授权边界**未登录、未注册**，因此 Dashboard State 的交互行为为 **NOT_OBSERVED**。
- **公开文档记载（非观察）**：仪表板由多个命名 state 组成，有一个 root state（如设备列表）和详情 state（如单设备）；widget 动作类型包括「Navigate to new dashboard state」「Update current dashboard state（只更新实体上下文，不切换 state）」「Navigate to other dashboard」；地图 widget 支持 marker/polygon 点击作为动作来源。
- **对本项目的适配（基于文档，需在授权环境验证）**：首页的「全国 → 区域 → 项目 → 设备」可视为同一页面的 state 序列；地图点击、列表点击、Attention 点击都应映射到同一组状态转换（本项目用 URL 参数 + Pinia 实现），而不是各组件各自持有选中状态。「只更新实体上下文」对应本项目在同一区域内切换选中资产（只改 `asset=`，不重新钻取）。
- **验证方法**：如 OWNER 提供 ThingsBoard 授权账号，可用同一 REA 场景格式记录 state 切换时的 URL 变化。

## 6. 既有文献结论（本轮未新增观察）

《Phase1-A1.1-R2-B4-数字孪生REA模式研究》中 DataV / ThingJS / Hightopo / AntV L7 / deck.gl / Cesium 的结论继续有效：拾取优先级、语义缩放、标签分层、InstancedMesh 状态色、IDLE 不空转。本轮没有在授权范围内访问这些商业平台的实际产品，不能作为新的「实际观察」。

## 7. 尚未观察 / 限制

- 真实 Keycloak + PostgreSQL 环境的首页（REA 未连接；B4 CI 环境为 Playwright 专用，未接入 REA）。
- 真实 GPU 下的帧时间与交互延迟（SwiftShader 不代表 GPU）。
- WebGL context loss 与 Reduced Motion 下的首页行为（本轮未构造）。
- Codex 中的 MCP 注册（需 OWNER 环境）。
