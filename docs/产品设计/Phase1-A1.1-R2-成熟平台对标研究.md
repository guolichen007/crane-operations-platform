# Phase1-A1.1-R2 成熟平台对标研究

> 本文档回答一个问题：**我们借鉴什么、我们不实现什么、为什么**。数据模型在概念上向成熟工业标准对齐，但**不照搬**、不实现完整标准。
> 研究手段：公开官方文档/规范 + rea-agents 6.8.0 对线上部署与竞品的静态反解；原始 evidence 归 `local-private/research/`（gitignore），仓库只放脱敏结论。

---

## 1. 工业资产模型标准

### 1.1 OPC UA for Machinery（OPC 40001）

核心 building blocks：Machine Identification/Nameplate、Component Identification/Nameplate、Machinery State、Operation Mode、Operation Counter、Lifetime Counter、Monitoring、Notifications。

| 借鉴 | 不实现 | 为什么 |
|------|--------|--------|
| 机器与组件分层识别（Identification/Nameplate）→ 我们的 Asset / Equipment 双层 | OPC UA 服务器/节点集/信息模型全栈 | 我们是云端资产图谱，不是现场 OPC UA 服务器 |
| Machinery State / Operation Mode → 四维状态里的 Connectivity/Condition + operating_mode | 直接接 OPC UA DA/A&C | A2 前不接真实 PLC/控制 |
| Operation Counter / Lifetime Counter → running_hours/work_cycles/motor_starts/brake_cycles 等累计量 | 完整 OPC 40001 类型体系 | 只取字段语义，用自有 schema 承载 |

### 1.2 IDTA Asset Administration Shell（AAS）

相关子模型：Digital Nameplate（IDTA 02006）、Plant Asset Management（IDTA 02019）、Asset Location（IDTA 02045）、3D Models、Control Component Type/Instance（IDTA 02015）。

| 借鉴 | 不实现 | 为什么 |
|------|--------|--------|
| Digital Nameplate 思想 → 设备铭牌字段（manufacturer/brand/model/serial） | AAS 完整 meta-model、submodel registry、REST 接口规范 | 过度；我们只需字段语义 |
| Plant Asset Management → 设备目录 + 安装 + 位置 | AAS 壳层/资产互联互通协议 | 工程量大，非本轮目标 |
| Asset Location → asset_locations 层级 + 坐标/坐标系统/来源 | AAS 位置子模型全量 | 取坐标+坐标系+来源+精度的核心字段 |
| Control Component Type/Instance → equipment_type / catalog_item / installation 的 type-instance 分离 | 完整 CC 子模型 | 取 type/instance 分层思想 |

### 1.3 AWS IoT SiteWise

Asset Model / Component Model / Hierarchy / Property（Measurement/Metric/Transform）。

| 借鉴 | 不实现 | 为什么 |
|------|--------|--------|
| Asset Model ≠ 设备实例（"模型"与"实例"分离）→ equipment_type/catalog 与 installation 分离 | 整套 SiteWise 服务/资产模型 XML/API | 我们是自建模型，不依赖 AWS |
| Measurement（原始）vs Metric（派生，带窗口）→ 遥测原始值 vs availability 派生值 | Transform 公式引擎 | 派生指标够用即可 |

### 1.4 ThingsBoard

Asset vs Device 分离 + Relations 层级。

| 借鉴 | 不实现 | 为什么 |
|------|--------|--------|
| Asset（厂房/区域/车辆）与 Device（产生遥测）分离 → 我们的 Asset / Runtime Binding | ThingsBoard 引擎/规则链/租户体系 | 语义借鉴，不换平台 |
| Relations 形成层级 → equipment_relations | 其图数据库引擎 | 关系用关系表实现 |

**统一结论**：我们实现"asset graph"的**概念内核**（Asset/Equipment/Runtime 分层、type-instance 分离、measurement/metric 区分、nameplate 字段、关系图），不实现任何标准的协议栈/信息模型/服务端。

---

## 2. 竞品设备/维护数据口径

### 2.1 Konecranes（yourKONECRANES + TRUCONNECT + CheckApp）

数据口径：设备信息、维护信息、**数字化检查结果归档一处**；TRUCONNECT Remote Monitoring 收集 running time / lifted loads / motor starts / work cycles / emergency stops / brake monitoring / inverter monitoring / 关键组件状态与预计寿命；TRUCONNECT Wire Rope Monitoring 连续监测钢丝绳（断丝/磨损/锈蚀，对标 ISO 4309:2017）；CheckApp 日常检查数字化。

| 借鉴 | 不实现 |
|------|--------|
| 检查结果数字化 + 归档一处 → inspections + documents/document_links | 其 OEM 专有采集硬件/云 |
| 运行数据口径（running/motor starts/work cycles/emergency/brake/inverter）→ telemetry metrics | 预测性维护算法（未验证不实现） |
| 钢丝绳连续监测 → wire_rope 专用四表 + inspection | 磁性探伤硬件 |

### 2.2 Demag Service Platform

跨厂家/跨地点设备管理，维护 + 文档 + 服务事件统一。

| 借鉴 | 不实现 |
|------|--------|
| 维护/文档/服务事件统一 → work_orders + documents + events | 其 OEM 服务履约体系 |
| 跨厂家/跨地点 → 多租户/多项目（我们已有） | — |

### 2.3 卫华「起重管家 3.0」、微特 WTAU

卫华全生命周期透明管理、一张网覆盖全国；微特钢丝绳无损探伤（永久存档备查）、寿命测算、点检台账、维保工单闭环、档案库 Excel 导出。

| 借鉴 | 不实现 |
|------|--------|
| 全生命周期档案 + 台账导出（安监/内审）→ XLSX export + 合规档案 | 其专有探伤/寿命算法 |
| 维保工单闭环 → work_order 状态机 + events | — |

---

## 3. 前端/产品方向（高级平台借鉴）

方向已定调：保留 Dark Premium，转高密度工作台（information density > glow/glass/大卡片），新增 Compact/Comfortable 两档 density，本轮不加 Light Theme。

- 数据表格：headless 表格 + 虚拟化 + 列可见性 + 服务端分页（Vue 3 对应 TanStack Table + vue-virtual）。
- 设备台账统一列：设备编号/类型/所属行车/位置/厂商/品牌/型号/序列号/安装日期/行政状态/实时连接/健康/固件/最近检查/下次维护，支持 LiDAR/Camera/PLC/IPC/Switch/VFD/Motor/Brake/Sensor 统一筛选。
- Asset 360 Tabs：概览/实时运行/数字孪生/系统拓扑/部署位置/设备/钢丝绳/告警/点检/维保/合规/软件版本/标定/资料/变更历史。
- 传感器 FOV/range 可视化、系统拓扑 2D graph、Command Palette（Ctrl+K）、DirtyGuard、乐观并发提示。
- rea baseline 证据（当前构建产物）：`three` 970KB、`charts` 1127KB 为最大 chunk → R2 需拆包/lazy/按需加载（对标研究基线）。

---

## 4. 借鉴落地清单（R2 范围内）

| 来源 | 落到 R2 |
|------|---------|
| OPC UA Machinery / AAS Nameplate | equipment_types + catalog + installation 铭牌字段 |
| OPC UA Counter | telemetry metrics（running_hours/work_cycles/motor_starts/brake_cycles） |
| SiteWise | type-instance 分离、measurement vs metric |
| ThingsBoard | Asset/Runtime 分离、relations |
| Konecranes | 检查归档、运行数据口径、钢丝绳监测 |
| Demag | 维护/文档/事件统一 |
| 卫华/微特 | 全生命周期档案、台账导出、工单闭环 |

**R2 不实现**：完整 OPC UA/AAS 协议栈、SiteWise 服务、预测性维护/寿命自动算法、OEM 探伤硬件、ERP 级备件/采购/财务、远程控制、设备 Enrollment、公网 MQTT 管理。

---

## 5. 参考来源

- OPC UA for Machinery（OPC 40001-1 Basic Building Blocks）：[OPC Foundation 在线参考](https://reference.opcfoundation.org/specs/OPC-40001-1/)
- IDTA AAS Submodel Templates：[Digital Nameplate IDTA 02006](https://industrialdigitaltwin.org/en/wp-content/uploads/sites/2/2024/11/IDTA-02006-3-0_Submodel_Digital-Nameplate.pdf)、[Plant Asset Management IDTA 02019](https://industrialdigitaltwin.org/en/wp-content/uploads/sites/2/2024/11/IDTA-02019-1-0_Submodel_PlantAssetManagement.pdf)、[Asset Location IDTA 02045](http://industrialdigitaltwin.io/idta-submodel-templates/IDTA-02045/1.0/index.html)、[Control Component Type/Instance IDTA 02015](http://industrialdigitaltwin.org/wp-content/uploads/2025/02/IDTA-02015-2-0-_Submodel_CCType.pdf)
- AWS IoT SiteWise 资产与组件模型：[AWS 文档](https://docs.aws.amazon.com/iot-sitewise/latest/userguide/create-asset-and-component-models.html)
- ThingsBoard Asset vs Device + Relations：[ThingsBoard 文档](https://thingsboard.io/docs/user-guide/assets/)
- Konecranes TRUCONNECT Remote Monitoring：[brochure](https://www.konecranes.com/sites/default/files/2024-09/brochure_truconnect_remote_service_2024_0.pdf)、[Wire Rope Monitoring](https://www.konecranes.com/sites/default/files/2024-09/brochure_truconnect_wire_rope_monitoring_2024.pdf)、[yourKONECRANES 维护信息](https://www.konecranes.com/en-ie/service/yourkonecranescom/maintenance-information-on-yourkonecranescom)
- Demag Service Platform：[德马格](https://www.demagcranes.com/en/services/demag-service-platform)
- 卫华「起重管家 3.0」：[河南卫华](https://www.henanweihua.com.cn/news/2166/)
- 微特·全生命周期运维：[wtauserver](https://www.wtauserver.com/full-lifecycle-operation-and-maintenance-management-system-for-lifting-equipment/)
- rea 逆向工具：[morluto/rea](https://github.com/morluto/rea)
