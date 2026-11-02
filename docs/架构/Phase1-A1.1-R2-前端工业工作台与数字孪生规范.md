# Phase1-A1.1-R2 前端工业工作台与数字孪生规范

> 目标：把现有 Vue3 + Three.js 0.186 + camera-controls + postprocessing + GSAP + ECharts 6 + Pinia + Router + Vite 用到**成熟工业平台水平**。不是推翻技术栈，而是让每 1KB 前端代码提供工业管理价值。DESIGN_ONLY=YES。

---

## 1. 五类 Workspace（不是一堆 Dashboard）

| Workspace | 核心目标 | 关键内容 |
|-----------|---------|---------|
| **Operations Center** | 全国设备在哪、谁告警、谁待检、谁离线 | Operations Map 默认；KPI 行；Asset/Site 卡 |
| **Asset Workspace** | 某台行车完整档案 | Asset 360（Header + Actions + 13 Tabs） |
| **Twin Workspace** | 物理孪生 + 实时状态 | Three.js Physical Twin + 设备 Inspector + 传感器 FOV |
| **Maintenance Workspace** | 点检、工单、维护、替换 | 台账 + 工单状态机 + 更换记录 |
| **History Workspace** | 历史趋势、事件、版本、运行回放 | History Explorer + Replay + Incident Review |

## 2. Global Shell（始终存在）

```
U·DAO  [项目范围 ▼]   [Ctrl+K 搜索…………]   LIVE|HISTORY   告警 12   [用户]
```

- 顶部全局栏：品牌 + Project Scope Selector + Ctrl+K 全局搜索 + **LIVE/HISTORY 模式切换** + 告警中心快捷 + 用户。
- 侧导航六域：运营中心 / 资产中心 / 运行监控 / 维保中心 / 合规中心 / 数据治理。
- A2 前不出现：远程控制、设备 Enrollment、公网 MQTT 管理入口。

## 3. Operations Center（默认 Operations Map，非 Three 全国沙盘）

- 默认 **Operations Map**（MapProvider 抽象）；KPI：总资产 / 实时在线 / 活动告警 / 待检查 / 开放工单 / 位置未配置。
- 地图点：真实 Asset/Site 位置；点击 → Asset Card（进入资产 / 实时状态 / 历史）——不再走「3D 地图→项目→设备列表→搜索→详情」长路径。
- **Three Space View 作为第二模式**：用户点「空间沙盘」才 dynamic import Three（MapSceneManager/postprocessing）。

## 4. Asset 360（最终布局）

```
┌─────────────────────────────────────────────────────┐
│ QD-001  20t 双梁抓斗行车   ACTIVE   ● ONLINE       │
│ 上海 / 1号车间 / 2跨                                  │
│ [编辑资产] [新增点检] [创建工单]                       │
├─────────────────────────────────────────────────────┤
│ 概览|实时|孪生|拓扑|设备|钢丝绳|告警|点检|工单|合规|    │
│ 标定|软件|历史                                        │
├─────────────────────────────────────────────────────┤
│                     Workspace                        │
└─────────────────────────────────────────────────────┘
```

- 不把所有内容同时铺开；每个 Tab 是独立 Workspace，进入才加载对应代码。
- Header：asset_code / name / asset_status / project / site / connectivity；Actions 按 capability 显隐。
- Data Completeness（Autodesk Tandem 思想）：档案/位置/Equipment/合规/标定/文档 用 COMPLETE/PARTIAL/MISSING，规则明确后才显示百分比。

## 5. Equipment Workspace（设备台账）

- 统一 DataGrid，列：设备编号/设备类型/所属行车/位置/厂商/品牌/型号/序列号/安装日期/行政状态/实时连接/健康/固件/最近检查/下次维护。
- server-side pagination/sort/filter/search/column visibility；Compact/Comfortable 两档 density。
- 支持 LiDAR/Camera/PLC/VFD/Motor/Brake/IPC/Switch/Encoder/Load Sensor/IMU/Limit/Safety Device 统一筛选。

---

## 6. Three.js Rendering Spec（Premium Industrial，不走游戏风）

| 项 | 规范 |
|----|------|
| Renderer | ACES tone mapping；`outputColorSpace` 正确；soft contact shadows |
| Material | PBR（MeshStandardMaterial）；metalness/roughness 合理 |
| Environment | HDR/IBL 环境光 + Directional Key + Ambient/Hemisphere |
| Background | 工业网格/地面，subtle |
| Selection | outline（非整车变色） |
| Alarm | 只对报警对象 emissive/脉冲，**selective bloom** |
| Bloom | **仅**报警/指示灯/选择焦点/关键运行态，禁止整台行车发蓝光 |
| Labels | 语义节点标签，稀疏、可开关 |
| Quality tiers | 沿用 QualityManager（premium/high/low），Reduced Motion 时关闭 postprocessing/粒子 |
| 禁止 | gaming neon、全屏 bloom、无业务意义粒子 |

## 7. 3D 交互（Twin Workspace）

- **Select / Focus / Isolate / Hide / X-Ray / Reset View**（交互语义借鉴 xeokit，自研实现）。
- 相机预设：Overview / Top / Side / Hoist。
- **Equipment Tree**（左面板，按 semantic_slot 树）；**Scene Search**（按设备编号/类型定位并 focus）。
- **Sensor FOV**、**Labels** 开关、**Alarm Focus**（跳转到告警设备）、**Measurement**（距离/尺寸，可选）、**Inspector**（右面板，点击设备联动）。
- 映射链：`three object → semantic_slot → asset_equipment_installations → runtime binding → telemetry`。

## 8. 传感器可视化（我们的特色）

- **LiDAR**：mount pose + coordinate frame + 水平/垂直 FOV + range volume（透明扫描区域）。
- **Camera**：mount pose + camera frustum。
- 点击 LiDAR/Camera → Inspector 出现开关：`[显示感知范围 ✓] [显示安装坐标系 ✓]`。
- **pose_source 纪律**：`CALIBRATED/SURVEY` → 标"精确安装位姿"；`MANUAL` → 标"示意安装位姿"。长期坚持。

## 9. 系统拓扑（2D graph，不塞 Three.js）

- Physical Twin（Three.js）与 System Topology（2D）分离。
- 拓扑域：Control / Network / Power / Sensor。例：`PLC→VFD→Motor`、`IPC→Switch→LiDAR→Camera`。
- **Vue Flow（MIT）候选，先 PoC**；若自有 SVG 足够则不强制引入（节点有限，SVG 更可控、无额外 bundle）。

## 10. 动画规则

- **GSAP**：页面进入 / camera focus / panel transition / 数字滚动 / alarm focus / timeline open。
- **Three render loop**：只做实时设备运动 / 状态效果 / camera update。
- **禁止 render loop**：创建 GSAP tween、DOM measurement、大量 reactive mutation（否则做大后掉帧）。

## 11. headless UI 决策

| 依赖 | 用途 | License | 决策 |
|------|------|---------|------|
| @tanstack/vue-table | DataGrid 状态（sort/filter/column/visibility） | MIT | 引入 |
| @tanstack/vue-virtual | 大行渲染（10k equipment） | MIT | 引入（不替代服务端分页） |
| Reka UI | Dialog/Popover/Dropdown/Tabs/Tooltip/Command/Focus management | MIT | 引入（仅 primitive，样式全 U·DAO） |
| Vue Flow | 系统拓扑 | MIT | 候选，PoC 后定 |

不引入：大而全 UI Framework、React、TresJS、AGPL（realvirtual/xeokit）、Open MCT 本体。

## 12. UX State 全定义（不许空白卡片）

每个页面/组件必须显式处理：`LOADING / EMPTY / ERROR / PARTIAL / READ_ONLY / NO_PERMISSION / NO_RUNTIME / NO_HISTORY / OFFLINE`。

- 无 runtime binding → 「实时状态未接入」；无可信 condition → 「未知」；无历史 → 「NO_HISTORY」；viewer → 无 write CTA；无权限 → 「当前项目为只读权限」。

## 13. 前端 QA 矩阵

Playwright 覆盖：1920×1080 / 1366×768 · Compact/Comfortable · viewer/operator/maintainer/platform_admin · Reduced Motion · WebGL off · slow API · empty/partial data · 10k equipment list · 30d history · Replay。视觉回归用 Playwright screenshot + rea compare_web_screenshots 基线：Operations Center / Asset Center / Asset 360 / Twin / Equipment Inspector / History Explorer / Maintenance / Compliance。
