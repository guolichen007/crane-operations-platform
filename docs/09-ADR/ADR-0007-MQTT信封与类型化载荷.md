# ADR-0007：MQTT Envelope 与类型化 Payload

状态：T0.5 采用，待人工整体审查。

旧 POC 大 Message 强制所有消息携带无关字段，无法清楚验证 Topic 与载荷含义。采用公共 Envelope 和每个 kind 的具体 Payload，由 contracts/model.json 单源生成 Go、TypeScript、JSON Schema、AsyncAPI。接收边界同时检查 Topic、身份、kind、Payload。

deviceId 是不可变全局技术 ID。bootSeq 与 sequence 判断新旧，sentAt 只观测。当前 crane/v1 尚未接真实设备，直接替换旧载荷，不维护双栈。T0.5 验收后视为 V1 Freeze Candidate，后续真实接入优先向前兼容扩展。
