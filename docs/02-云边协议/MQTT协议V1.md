# MQTT 协议 V1

单一事实源 contracts/model.json；node tools/codegen/contracts.mjs 生成 Go/TS 类型、JSON Schema 和 AsyncAPI，--check 检查漂移。契约更改须审查兼容性。
前缀 crane/v1/{tenantId}/{projectId}/{deviceId}/，topic 由 contracts/go 构造和解析。标识符仅字母数字下划线连字符，最大 80，不接受通配符、路径或任意 topic。
heartbeat 与 telemetry/live QoS 0；shadow/reported、alarm、event、watch start/renew/stop、command/ack、event/ack QoS 1。Shadow retained，BIRTH/SNAPSHOT/LWT/DEATH 在该 topic。
T0.5 使用 Envelope + 类型化 Payload。Envelope 为 schemaVersion、messageId、tenantId、projectId、deviceId、bootId、bootSeq、sequence、sentAt、kind、correlationId、payload。deviceId 是不可变全局技术 ID。sentAt 仅观测；顺序只比较持久 bootSeq，再比较 sequence。旧 boot 的延迟消息全部拒绝。
每个 Topic 在 AsyncAPI 中引用对应消息 Schema；Go Decode 同时校验 Topic、身份、kind、Payload 和必填字段。旧平铺 Message 不兼容、不双发。详见 T0.5 工程说明和故障语义。
TLS 校验 CA、主机和证书；开发每台证书 CN 对应 MQTT username；ACL 限定 demo/Project-A/自身设备，不能写 watch 或读取他台。Cloud 专用证书访问固定开发 namespace。生产动态租户 ACL 另行评审。
MQTT_CONNECTED、EDGE_HEALTHY、CONTROLLER_HEALTHY 分开；LWT 标 OFFLINE，健康改 UNKNOWN。不要将连接在线等同于机械健康。

Presence 与 ReportedState 分开；LWT/DEATH 不清空最后业务状态。Will Delay 默认 20 秒、范围 5–60 秒；Clean Start=false、Session Expiry=3600 秒，使短暂重连继续同一会话并取消尚未发布的 Will。WATCH 有不超过 TTL 的 Message Expiry，仍须 generation fencing。
