# MQTT 协议 V1

单一事实源 contracts/model.json；node tools/codegen/contracts.mjs 生成 Go/TS 类型、JSON Schema 和 AsyncAPI，--check 检查漂移。契约更改须审查兼容性。
前缀 crane/v1/{tenantId}/{projectId}/{deviceId}/，topic 由 contracts/go 构造和解析。标识符仅字母数字下划线连字符，最大 80，不接受通配符、路径或任意 topic。
heartbeat 与 telemetry/live QoS 0；shadow/reported、alarm、event、watch start/renew/stop、command/ack QoS 1。Shadow retained，BIRTH/LWT 在该 topic。
每条消息 schemaVersion=1、messageId、tenant/project/device、bootId、sequence、UTC timestamp、kind。一个 boot 内递增序号去乱序；新 boot 仅新 BIRTH 建立。事件 message_id 唯一去重。
TLS 校验 CA、主机和证书；开发每台证书 CN 对应 MQTT username；ACL 限定 demo/Project-A/自身设备，不能写 watch 或读取他台。Cloud 专用证书访问固定开发 namespace。生产动态租户 ACL 另行评审。
MQTT_CONNECTED、EDGE_HEALTHY、CONTROLLER_HEALTHY 分开；LWT 标 OFFLINE，健康改 UNKNOWN。不要将连接在线等同于机械健康。
