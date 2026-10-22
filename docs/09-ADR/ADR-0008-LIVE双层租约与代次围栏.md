# ADR-0008：LIVE Viewer Lease 与 Device Upstream Lease

状态：T0.5 采用，待人工整体审查。

每个 Viewer 独立持久租约，同设备共享一个 upstream。PostgreSQL live_sessions、device_live_state 是期望状态权威；device_commands 仅保存 WATCH 待发与应用结果。201 不代表 Edge 已执行。

Edge 持久 generation/leaseId 和有界命令去重。START 使用更高 generation，RENEW/STOP 使用当前 generation。Cloud 在 BIRTH 和连接恢复时主动对账，以新 generation 隔离积压命令。只有匹配当前请求且确认实际 Edge 状态的 WATCH_ACK 能推进 LIVE。保留现有 TTL/grace，避免框架、分布式锁和新增中间件。
