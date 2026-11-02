# Phase1-A1.1-R2 范围边界与能力分层

## 1. R2 MUST

### 项目与交付

Project Intake、Portfolio、项目生命周期、里程碑、活动、资源日历、预约、冲突、风险、问题、变更、调试、试运行、验收、Punch、Handover、质保。

### 资产

Asset、Location、Equipment、Catalog、Supplier、Warranty、轻量 Spare Part、Documents metadata。

### 运行

现有 Shadow/LIVE/Alarm 扩展为 Equipment runtime context；不引入真实远控。

### 可靠性

Inspection、Maintenance Plan、Work Order、Wire Rope、Calibration、Software/Firmware History、Replacement。

### 历史

Revision、Timeline、Telemetry Metric Catalog、History API、Configuration@T、Replay、Incident Review。

### 前端

Industrial Shell、Portfolio Workspace、Operations Center、Asset 360、Equipment Center、Maintenance、Compliance、History、Twin、Global Search、Attention Center。

### 治理

Capability、Portfolio Scope、Project Scope、Audit、Optimistic Concurrency、Idempotency、Import/Export Jobs、Migration/Backup/Restore。

## 2. R2 LATER（接口预留，不深做）

- 预算、采购、发票、财务；
- 完整仓储/WMS；
- 合同结算；
- 完整 CPM/关键路径优化器；
- 自动 resource leveling；
- AI 生命周期助手；
- 短信/邮件/微信通知；
- 自动预测性维护/寿命算法。

## 3. A2_PLUS（严格禁止提前进入）

- 真实 Edge Enrollment；
- one-time claim / CSR / unique X509；
- 证书轮换/吊销/重置；
- 公网 Crane MQTT；
- 真实设备 provisioning；
- Remote Control；
- PLC command。

## 4. 依赖控制

R2 不引入：Redis、Kafka、Kubernetes、Elasticsearch、InfluxDB、ClickHouse、TimescaleDB、新微服务、React、TresJS、大型 UI Framework。

任何新依赖必须在 ADR 记录：用途、许可证、bundle/运行成本、替代方案、退出策略。

