# Phase1-A1.1-R2 外部研究来源与许可证

> 这里只记录公开研究来源。产品实现不得因为“参考”而复制受限代码或品牌素材。

## 工业资产 / IoT

- OPC UA for Machinery OPC 40001-1：Machine/Component Identification、State、Operation Mode、Counters、Monitoring。https://reference.opcfoundation.org/specs/OPC-40001-1/full
- ThingsBoard Assets：Asset 与 Device 分离、Relations、Maps、Audit/History。https://thingsboard.io/docs/user-guide/assets/
- OpenRemote Manager UI：Map、Asset Tree、Attributes、Location、History、编辑模式。https://docs.openremote.io/docs/user-guide/manager-ui/
- AWS IoT SiteWise：Asset/Component model、hierarchy/property（概念参考）。
- IDTA AAS：Digital Nameplate、Plant Asset Management、Asset Location、Control Component 等子模型（语义参考）。

## Crane / EAM

- Konecranes Portal / TRUCONNECT / Wire Rope Monitoring：设备、检查、维护、运行指标、钢丝绳。
- Demag Service Platform：跨厂家/跨地点设备、维护/服务事件/文档。
- IBM Maximo PM：time/meter/hybrid、fixed/floating、PM hierarchy。https://www.ibm.com/docs/
- SAP S/4HANA Maintenance：Planning/Approval/Preparation/Scheduling/Execution/Completion 等阶段。https://help.sap.com/
- 卫华/微特：国内行车全生命周期/钢丝绳/台账导出业务口径，作为市场参考，不作为法规来源。

## Portfolio / Scheduling

- Oracle Primavera Cloud Portfolio Resource Planning：resource demand/availability、time-phased allocation、scenario、overallocation。https://docs.oracle.com/
- PostgreSQL Range Types：`tstzrange` / GiST / exclusion constraint 可表达不重叠区间。https://www.postgresql.org/docs/current/rangetypes.html

## 前端 / 可视化

- realvirtual-WEB：AGPL-3.0，只学习 browser 3D HMI/GLB/设备信息/信号绑定模式，不复制源码。https://github.com/game4automation/realvirtual-WEB
- xeokit-sdk：AGPL-3.0，只学习 Select/Focus/Isolate/X-Ray/Tree 交互。https://github.com/xeokit/xeokit-sdk
- Autodesk Tandem：3D + Asset/Stream/Chart/Data Completeness 工作区思想。https://intandem.autodesk.com/
- NASA Open MCT：Time Conductor/telemetry analysis 模式；LICENSE=Apache-2.0（仓库 LICENSE.md 权威；GitHub API classifier 曾 NOASSERTION）。https://github.com/nasa/openmct
- TanStack Table/Virtual/Query：MIT。https://tanstack.com/
- Reka UI：MIT。https://reka-ui.com/
- MapLibre GL JS：BSD-3-Clause。https://maplibre.org/maplibre-gl-js/
- Three.js：MIT；GLTFLoader 支持 Meshopt/KTX2/Draco。https://threejs.org/docs/pages/GLTFLoader.html
- Apache ECharts：Apache-2.0。

## 合规

- 市场监管总局第74号令：gov.cn/samr.gov.cn 官方文本；起重机械安全总监/安全员、日管控/周排查/月调度等以官方条款为准。
- TSG 08—2026、TSG 51—2023：字段级实现前继续核验正式文本，不用博客补条款。

## License 红线

- AGPL：只学模式，不复制源码/衍生集成；
- 商业 SaaS：只研究公开交互和公开文档；
- 第三方图片/品牌素材不进入产品；
- 新 npm/go dependency 在 BATCH 实现前再次核验当前许可证和版本。
