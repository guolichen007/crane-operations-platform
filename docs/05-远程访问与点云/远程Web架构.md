# 远程 Web 架构

Phase 2 目标 Browser → Cloud Auth/RBAC/Session → Gateway → 内部 FRP → Local Web。FRP 仅传输，不能提供公网直通现场地址。
Session 绑定 user、device、TTL、idle timeout、source IP、audit id，远程至少 maintainer。短期授权不能暴露 tunnel 凭据或内部地址。
Phase 0 Gateway 仅健康检查，业务 403；Cloud remote API 501。Compose 无 FRP 运行服务、无 Gateway 公网端口。审核设备兼容与安全设计后再实现反向代理、WebSocket Upgrade 和完整审计。
deploy/frp 记录拓扑边界，不存真实现场地址或 token。
