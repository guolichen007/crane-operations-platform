# Phase1-A1.1-R2 工业工作台 UI/UX 与前端技术规范

## 1. 目标

保留现有 Dark Premium 品牌感，但从“发光大屏”转为“高密度、可编辑、可追溯、低延迟的工业工作台”。视觉服务于业务状态和操作反馈，不以特效数量作为高级感。

## 2. 前端状态分层

建议把状态拆成三类：

### Client / Workspace State → Pinia

当前项目范围、当前选中 Asset/Equipment、侧栏、density、主题、Time Context、Twin 工具开关、最近访问等。

### Server State → TanStack Query（B4 评估后建议引入）

列表、详情、分页、缓存、request dedupe、mutation pending、query invalidation、后台刷新。避免把 HTTP cache/加载状态全部塞进 Pinia。

### High-frequency Runtime State → RuntimeFrameAdapter

20Hz/更高 WebSocket 不进入大规模 Vue reactive tree；Three rAF 直接消费最新 frame；UI 以 2~5Hz projection；Chart 批量 1~2Hz。服务器历史不因 display decimation 丢样本。

## 3. Global Shell

```text
┌ U·DAO ─ Project/Portfolio Scope ─ Ctrl+K ─ Attention ─ User ┐
│ Side Navigation                                             │
│                                                            │
│                         Workspace                           │
└────────────────────────────────────────────────────────────┘
```

侧导航：

- 运营中心；
- 项目交付；
- 资产中心；
- 运行监控；
- 维保中心；
- 合规中心；
- 数据治理。

Time Conductor 只在实时/Twin/History/Incident 等有时间语义的工作区出现，不全局常驻。

## 4. Workspace 体系

### Operations Center

KPI：资产总数、在线、活动告警、待检查、开放工单、项目冲突、即将进场、位置未配置。

默认轻量 Operations Map / Site list；Three Space View 为 opt-in，不能首屏默认加载 Three。

### Portfolio Workspace

Portfolio Overview、Project Register、Gantt/Calendar、Resource Heatmap、Conflict Center、Project Detail。

### Asset Workspace

Asset Center + Asset 360；真实 CRUD、Location、Equipment、Wire Rope、Compliance、Documents。

### Maintenance Workspace

Attention、Inspection、PM、Work Order、Replacement、Calibration、Warranty。

### Twin Workspace

Physical Twin、Equipment Tree、Inspector、System Topology、runtime/history context。

### History Workspace

History Explorer、Timeline、Revision Compare、Replay、Incident Review。

## 5. Asset 360 信息架构

禁止 13 个横向 Tab 挤在 1366 屏。采用六个一级域：

1. 概览；
2. 运行（实时/孪生/历史/告警）；
3. 资产（Equipment/钢丝绳/部署位置/软件固件/标定）；
4. 维保（点检/工单/维护计划/更换）；
5. 合规（登记/检验/日周月/技术档案）；
6. 资料与变更（文档/Revision/Audit）。

Header 永远显示 asset_code、名称、状态、Project、Site、Connectivity；CTA 按 capability。

## 6. DataGrid 规范

使用 TanStack Table 管列/排序/筛选状态；大页面可用 TanStack Virtual，仅降低 DOM，不替代 server-side pagination。

必须具备：
- server paging/sort/filter/search；
- rowCount；
- column visibility；
- sticky header；
- row selection（仅真正需要批处理页面）；
- Compact / Comfortable density；
- empty/error/loading；
- permission-aware actions；
- URL 保存关键 filter/sort/page（可分享/返回）。

数据规模：10k Equipment 时浏览器不应一次拉全量。

## 7. Server-state 请求规范

如果引入 TanStack Query：
- queryKey 必须包含 scope + resource + filters；
- mutation 成功只 invalidate 相关 query；
- 409 不做 optimistic overwrite；
- 路由切换取消无用请求；
- 资产详情可保留 stale data + skeleton 局部刷新；
- 不把敏感 query cache 持久化到 localStorage。

## 8. 表单与编辑

基础组件：FormField、NumberField、DateField、Select、EntitySelector、LocationSelector、ManufacturerSelector、EquipmentTypeSelector、ValidationSummary、DirtyGuard。

原则：
- 创建复杂资产用 Wizard，不用超大 Modal；
- 编辑详情优先 Drawer/Workspace；
- `version/If-Match` 冲突返回 409，显示差异/刷新提示；
- saving 禁止双击；
- destructive action 二次确认；
- retire 与 delete 文案严格区分。

## 9. Project UI

### Project Intake Wizard

步骤：基础 → 客户/Site → 项目类型与范围 → 时间要求 → 团队 → 预计设备 → 风险/限制 → Review。

### Gantt/Calendar

使用 ECharts Custom Series/Range Bar 表达：baseline、current plan、actual、milestone、dependency、conflict overlay。不做桌面工程软件式复杂拖拽依赖编辑器第一版。

### Resource Heatmap

资源 × 周，显示 demand/capacity；>100% 红色；点击进入 reservation 冲突详情。

### Conflict Center

BLOCKING/WARNING/INFO 分栏；解决操作：调整时间、换资源、拆活动、受控 override。

## 10. Operations Map

MapProvider interface：marker、cluster、popup、fitBounds、select、quick entry。

位置层级：
- L1 全国/区域；
- L2 Site/Workshop（优先 LOCAL_SITE）；
- L3 Asset/Twin。

真实坐标只来自明确配置；行政区中心不能画成 Asset marker。

## 11. Global Search

Ctrl+K 搜：Project、Site、Asset、Equipment、Serial、Wire Rope、Work Order、Inspection、Alarm、Document、Risk、Issue、Change。

结果显示 type/status/project/location；后端按 Portfolio + Project scope 过滤。支持最近访问、键盘上下选择和 Enter 打开。

## 12. Attention Center

统一展示项目进场/延期/冲突、检验/维护/标定/质保/文档到期、开放告警。提醒本身不是 canonical 数据；关闭提醒不能修改真实过期事实。

## 13. Design Tokens

建议保持现有 brand 深蓝，但把视觉 token 明文化：

- surface-0/1/2/3；
- border-subtle/strong；
- text-primary/secondary/muted；
- action-blue；live-cyan；normal-green；warning-amber；alarm-red；unknown-gray；
- spacing 4/8/12/16/24/32；
- radius 6/10/14；
- motion fast 120~160ms / standard 180~240ms / camera 350~650ms；
- Reduced Motion 时移除非必要位移动画。

Glass 只用于浮层/悬浮 Inspector，不用于每一个表格卡片。

## 14. UX 状态

所有功能必须定义：LOADING、EMPTY、ERROR、PARTIAL、READ_ONLY、NO_PERMISSION、NO_RUNTIME、NO_HISTORY、OFFLINE。

禁止空白卡片、无限 spinner、错误后无 retry。

## 15. 可访问性

使用 Reka UI primitives 管 Dialog/Popover/Tabs/Tooltip/Command/focus；语义 button/table；键盘可操作；焦点可见；颜色状态不能只靠颜色；表单错误与字段关联。

## 16. 前端依赖决策

- 保留 Vue/Router/Pinia/Three/GSAP/ECharts；
- TanStack Table/Virtual：批准；
- TanStack Query：B4 建议引入，负责 server-state；MIT；
- Reka UI：批准 primitives；
- Vue Flow：B7 PoC；30 nodes/100 edges 通过性能/键盘/bundle 后才引入；
- MapLibre：MapProvider candidate；生产 tiles/许可后置；
- 不引入 React/TresJS/大型 UI Framework。

## 17. 路由与 lazy

所有主 Workspace 使用 Vue Router dynamic import；Three/ECharts heavy feature 在二级功能内 lazy，而不是仅靠 route vendor chunk。普通 Asset 台账不能因为依赖包存在就加载 Three/完整 ECharts。

