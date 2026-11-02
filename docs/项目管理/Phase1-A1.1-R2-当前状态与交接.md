# Phase1-A1.1-R2 当前状态与交接

## 当前阶段

- Phase：Phase1-A1.1-R2；
- 当前工作：B1 Data / Contract Foundation（本批代码实施中）；
- REMOTE_MAIN_BASE=29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18；
- 功能分支：`feature/phase1-a1-1-r2-asset-operations`；
- G0_R1_REVIEW_HEAD=725458b6953a48b9aa10a3fa62bb499867e3adad；
- B1_BASE_SHA=725458b6953a48b9aa10a3fa62bb499867e3adad；
- G0_R1_REVIEW=PASS；
- R2_DESIGN_FROZEN=YES；
- B1_STARTED=YES；
- B2_STARTED=NO；
- PHASE1_A2_STARTED=NO；
- PRODUCTION_CHANGED=NO。

## 已冻结方向

七大业务域；Asset→Equipment→Runtime→Telemetry；Portfolio/Project 双域权限；历史六层；Configuration@T；Industrial Workspace；Semantic Twin；分批审查制度。

## 环境

- Node 22.23.4：AVAILABLE；
- Go 1.27.3：AVAILABLE（用户态）；
- REA 6.8.0：AVAILABLE；
- Docker daemon：UNAVAILABLE_HARD_LIMIT（当前 CC 沙盒）；
- PostgreSQL：USERSPACE_INSTALL_FEASIBLE，B1 前 probe/freeze；
- Browser/Playwright：USERSPACE_PROBE_REQUIRED。

工具不存在不能直接等同 NOT_RUN：先安全用户态安装/验证；Docker 属真正硬限制，权威 Compose 交 CI。

## Git

本地 `docs/会话上下文.md` 持续更新但 DO NOT COMMIT。OWNER 手动 push feature，GPT 负责人审查后才能下一个 Batch。

## 生产边界

B1~B9 不改生产服务器/数据库/TLS/DNS/Firebot；B10 isolated rehearsal；A2 设备接入/远控全部禁止提前。

## 下一步

1. OWNER 手动 push feature 到 origin；
2. G0-R1-R1 GitHub/GPT 独立审查；
3. G0_R1_REVIEW=PASS 后才开放 B1（此前禁止创建 00005/00006、修改 contracts/Go/Vue/Three/package 依赖）。

