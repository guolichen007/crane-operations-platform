# Phase1-A1-R1 部署前门禁

基线 b8bed8e64ae9114ab49324e87ccd45cc4c32506b；沿用 feature/phase1-a1-production-web。不重构 OIDC/BFF、Session/CSRF、Project Scope、四角色、LIVE 事务授权或 WS 撤权。未部署服务器、未合并 main、未改真实 DNS/TLS/Firebot，不进入 A2。

## 变更与反证

验证入口 `node tools/task.mjs preflight-test`，用真实隔离 Git checkout 和模拟 Docker executor，绝不在正式服务器执行 --apply。tests/contract/production-preflight.test.mjs 覆盖：

- manifest SHA 与 HEAD 不同，拒绝且不执行 Docker。
- tracked deployment file dirty，拒绝且不执行 Docker。
- exact clean SHA 通过；Compose 路径锚定脚本 checkout，显式 project=u-dao-crane-ops。
- pull 期间 tracked 文件变化，在 up 前拒绝。
- 另一个空闲路径不能冒充 daemon DockerRootDir。
- Docker data-root 与 backup root 独立 statfs；任一不足10GiB或15%拒绝。
- Docker info 失败/目录不明、备份目录缺失、降低阈值均拒绝。
- 远程 Docker context 不能冒用本机同名目录的文件系统。
- 旧 Compose project 被拒绝。
- 全部 Dockerfile FROM 使用可读版本与 digest。

基础镜像固定到已验收 A1 CI 实际解析的 digest，不引入新镜像体系；构建后仍跑 Trivy HIGH/CRITICAL，不以 pin 替代漏洞扫描。正式 release 的 source SHA、Cloud/Web registry digest 和两份 SBOM 必须重新生成，最终只认本补丁 HEAD 的完整8项 CI。Artifact attestation 列下一轮建议，本轮不引入 Cosign/KMS。

首次部署顺序及严格 HTTPS discovery 见生产部署手册（A1.1-R1 补充 external frontend / shared ingress / Firebot 回归后再启动 Cloud/Web）。/srv/u-dao/platform/docker 仅是当前服务器事实，代码始终读取 daemon 实际 DockerRootDir。secrets/backup 既有目录路径不等于 Compose project，不做服务器目录或 Docker daemon 配置变更。
