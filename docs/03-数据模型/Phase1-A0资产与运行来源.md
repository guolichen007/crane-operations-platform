# Phase 1-A0 资产与运行来源

## 三层关系

Project 是业务项目；AssetDevice 是行车资产；RuntimeDevice 是 Cloud/MQTT 端身份。资产生命周期只有 PLANNED / PRE_DEVELOPMENT / COMPLETED；运行来源只有 SIMULATION / REAL / TEST。运行身份不能同时承担项目、资产和证书身份。

00003_asset_inventory.sql 是新增迁移，未修改00001或00002。projects 增加行政区和计划元数据，asset_devices 允许型号/吨位/跨度/工作级别/抓斗为空，devices 增加 nullable asset_device_id 与 source_mode。复合外键防止资产关联到错误项目；同一资产同一来源仅一个身份。现有未登记 seed 的 lifecycle 为 NULL，不进入 Inventory API。

SIM-P001-01 与未来 EDGE-P001-01 可以关联同一个 ASSET-P001-01，但证书、历史数据和运行身份各自独立。导入事务禁止改变已登记运行端的项目/资产/来源，不能把SIM直接改成REAL；应创建新的EDGE身份。当前Mock只接受SIMULATION，不启动REAL身份。

## 私有数据

仓库只提交 deploy/inventory/inventory.example.yaml，使用匿名Project-A等名称与稳定技术编号；公开示例故意不配置真实地点、型号和项目组合。文件采用JSON兼容YAML语法；解析器也接受普通YAML，拒绝未知字段、重复键、多文档、重复ID、越界值和错误关联。

真实Inventory放在local-private/inventory/actual-projects.local.yaml或服务器的私有目录，均不得进入Git或镜像。运行时可通过INVENTORY_FILE显式导入。只输出计数和错误类别，CLI不打印客户字段或数据库凭据。私有Inventory中的三项已完成项目仅登记Project，未提供设备资料时不建立任何资产或运行端。

导入先校验生成契约，再校验关联、生命周期、ID和来源。PostgreSQL事务与导入锁保证原子性；重复导入只更新描述元数据，不重复建行、不覆盖Shadow、Telemetry、Alarm或租约。只有无设备且未登记Inventory的初始Project-A开发seed可被清除。

## API 与页面

新增GET /api/v1/projects和GET /api/v1/assets/devices，仍使用现有开发身份与生成契约，不增加CRUD或临时Auth接口。GET /api/v1/devices继续返回Runtime Shadow；配置FLEET_FILE时仅显示该清单的运行身份，历史记录保留在数据库中。

项目API返回生命周期、行政区、计划及资产/运行/真实在线/模拟数量。资产API返回nullable技术参数和当前选用的运行来源；同时登记REAL与SIMULATION时优先展示REAL，两个运行端的历史不会合并。

API首页和项目/资产台账读Inventory API，不使用real-projects.ts填充。/runtime保留运行缓存列表，/devices/{id}继续查看Shadow和申请LIVE。未登记资产的旧测试端明确标为历史/测试运行源。完成但尚未接云的项目显示“待接入Cloud · 设备档案待补充”，不判作故障离线。

地图复用现有3D引擎；只有adminCode在本地GeoJSON清单中可解析时才显示行政区中心。未配置或不支持的行政区不造点，不根据名称推断地址，不生成现场经纬度。规划/预开发、已完成待接入、真实在线分别使用金/蓝/绿标记。地图位置只代表行政区，不能称为现场位置。

Demo/离线分支保留原匿名沙盘、Mock告警和动画；monitor/analytics/maintenance/remote仍明确为DEMO。本阶段没有OIDC、生产证书生命周期、真实PLC/ROS、Remote Web、点云或远控。
