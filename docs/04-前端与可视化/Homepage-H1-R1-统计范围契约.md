# 首页统计范围契约（H1-R1-2）

> 适用：`/overview` 运营中心首页（Demo 与 API 两种数据模式，二维与三维两种视图）。
> 实现：`apps/web-console/src/scene/home-metrics.ts`（`scopeProjects` / `inRegion` / `scopeLabel` / `homeDataState`）、`apps/web-console/src/stores/operations.ts`（`scope` / `scopedProjects` / `scopedDevices` / `scopedVisibleDevices`）。
> 测试：`apps/web-console/tests/home-state.test.ts`（纯函数）、`tests/e2e/premium/homepage-h1-r1.spec.ts`（真实浏览器）。
> 本契约只约束前端如何从已拉取数据计算首页数字；H2 的后端统一统计接口必须实现同一口径（见 H1-R1 验收报告 §7）。

## 1. 五个范围维度

| 维度 | 定义 | 来源 | 是否进入 KPI 基础集 |
|---|---|---|---|
| 全部授权项目 | 当前会话被授权可见的 Inventory 项目及其设备 | API 只返回授权数据（服务端过滤）；Demo 为本地全部演示数据 | 是（隐含前提） |
| 当前来源 | `全部展示` / `登记项目`（`dataSource=real`）/ `演示设备`（`dataSource=demo`，仅 Demo 模式） | 左栏「数据来源」，store `source` | **是** |
| 当前行政区域 | 当前**已显示**的钻取路径 `regionPath`（省→市→县），项目 `adminPath` 以其为前缀即属于该区域 | 地图钻取 / `?region=` / 行政区导航 | **是** |
| 当前设备状态 | `ALARM` / `OFFLINE` / `NOT_COMMISSIONED` / `REAL_RUNNING` / `UNLOCATED` / `planned` 等过滤 | KPI 卡、需要关注、`?status=` | **否**：只决定地图与 Inspector 列表显示哪些设备 |
| 当前聚焦项目/设备 | `?project=` / `?asset=` 选择 | Inspector、地图点击、深链 | **否**：只决定 Inspector 详情与相机 |

**KPI / 需要关注 基础集 = 全部授权 ∩ 当前来源 ∩ 当前行政区域。**

推导规则：

1. 状态过滤不使 KPI 自我过滤。点击「告警设备」后，告警 KPI 仍是该范围内的告警设备数，其他 KPI 也不变；变化的只有地图与 Inspector 列表。
2. Inspector 过滤列表 = 基础集 ∩ 状态过滤。标题显示**完整总数**；渲染行数最多 60，超过时显示「列表显示前 60 台，共 N 台」，总数不得被截断。
3. 行政区使用**已显示**层级（`regionPath`），不使用目标层级（`regionTarget`），因此钻取动画过程中数字不会提前跳变，且数字与地图、面包屑始终一致。
4. 无空间位置（无 `adminCode`）的项目只计入全国范围；进入任何省/市后不计入。
5. 选中项目或设备不改变任何 KPI。
6. 「登记行车」（Demo）= 基础集内 `dataSource=real` 项目的计划台数之和，同样随来源与区域变化。

## 2. 例外（显式标注，不静默混用）

| 数字 | 范围 | 原因 | 页面如何说明 |
|---|---|---|---|
| 告警卡副文「最近告警记录 N 条」与事件条 | 全部授权范围最近 ≤100 条记录 | `/api/v1/alarms` 无项目/区域维度的统计契约，前端不对记录做二次归属推断 | 卡片 `title` 说明「不随来源/行政区变化」；文案注明「非未处理数」 |
| 页面底部 Cloud Runtime 摘要 | Cloud Shadow 全量，独立 15 s 轮询 | 不同数据源与刷新周期 | 移到事件条之下，标注「独立轮询 15 s · 不刷新首页指标」 |

## 3. 标签与数值同步

KPI 行上方页头显示 `统计范围 <来源> · <区域>`，与数值由同一个 `store.scope` 计算：

- API：`全部授权项目 · 全国` / `全部授权项目 · 浙江省`
- Demo：`全部展示（含演示） · 全国` / `匿名登记项目 · 浙江省` / `演示设备 · 浙江省`

## 4. KPI 点击行为

| 卡片 | 行为 |
|---|---|
| Inventory 设备（API） | 进入 Inventory 设备清单 `/devices`，携带当前区域 `?region=`，清单数量 = 卡片数字 |
| 真实运行 / 告警设备 / 离线 / 尚未投运 | 应用对应状态过滤（再次点击取消），写 `?status=` |
| Demo 设备总数 | 清除状态过滤 |

## 5. 数据状态（H1-R1-1）

数字只能来自一次成功的拉取。

| 状态 | 条件 | KPI | 需要关注 | 页头 |
|---|---|---|---|---|
| `loading` | 尚无成功结果，请求进行中 | `—` | `—` | 正在拉取 Inventory API… |
| `unavailable` | 尚无成功结果，最近一次请求失败 | `不可用`（无任何数字） | `不可用` | Inventory API 拉取失败 · 暂无可用数据 |
| `ready` | 最近一次请求成功 | 数字 | 数字 | 最近拉取 <时间> · 来源 Inventory API |
| `refreshing` | 有旧结果，新请求进行中 | 旧数字 | 旧数字 | 最近拉取 <时间> · 正在更新… |
| `stale` | 有旧结果，最近一次请求失败 | 旧数字 + 「已过期」 | 旧数字 + 「已过期」 | 最近成功拉取 <时间> · 更新失败，数据已过期 |

只要数据来自一次**完整成功**的拉取（`ready` / `refreshing` / `stale`），任何指标的合法计数都可以是 `0`（例如成功拉取后告警设备为 0、或成功返回空列表时全部为 0）；只有**未知或拉取失败**（`loading` / `unavailable`）才不得显示数字。

> H1-R1 收口更正：初版此处写成「成功返回空列表是唯一允许显示 0 的情况」，表述错误，已按上述规则更正；代码行为本就如此（`hasNumbers()`），未改变。

## 6. 时间语义（H1-R1-3）

`fetchedAt` 是客户端最近一次成功拉取 Inventory API 的时间，**不是**设备观测时间。在后端提供 `observedAt` 契约之前，页头只能写「最近拉取」并注明来源，不得写「数据截至」。告警事件时间的时区问题（H0-D2）仍为 H2 项，反证保持 expected-fail。
