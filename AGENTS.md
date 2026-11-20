# 项目报备中心（Baobei）工程入口

仅适用于 `product/baobei-v1` 独立 worktree。Crane 及其他产品的规则不在此文件中，也不得被此文件改写。

## 现行状态

- **V1.1.0 已正式发布**：Tag `baobei-v1.1.0` → `805a70be94e3cc53857480248adde766d4a03e5b`（CI 44500169629 a1 8/8，Schema 00016，Realm baobei）；Release：https://github.com/guolichen007/crane-operations-platform/releases/tag/baobei-v1.1.0 。
- **模式**：`MODE=PRODUCTION_MAINTENANCE`，`FEATURE_FREEZE=YES`，`DEVELOPMENT=CLOSED`。
- **最终验收依据**：Release 附件 `baobei-v1.1.0-acceptance-signed.md`（即 57 号，OWNER 2026-11-19 签署 GO）。
- **事实源**：`docs/项目报备中心/00-项目总览与SSOT.md`（运行状态）；UI 动作事实源为 `交互动作矩阵.md`。

## 允许的工作

- **允许**：真实缺陷修复、安全补丁、必要运维。流程见 `docs/项目报备中心/61-V1.1维护准则.md`。
- **安全补丁与功能分开**：安全补丁不得夹带功能改动。
- **版本号**：下一次必要补丁为 `baobei-v1.1.1`。
- **下一版本**：V1.2 只维护需求候选清单，必须由 OWNER 明确批准后才能启动，不得自行创建阶段分支。

## 禁止事项

- 不移动或覆盖任何已有 Tag，不修改已发布的 Release，不重写 Git 历史，不合并 main 或 R2，不归档整个共享仓库。
- 助手不主动执行 git commit、push、打 Tag 或创建 Release；只有 OWNER 当次明确授权时才可以做。
- 不自动登录、不操作生产服务器；服务器操作由服务器 CC 按书面说明执行。
- 生产凭据、会话上下文、日志、数据库备份、截图原件、`local-private/` 一律不提交仓库。

## 工程约束（沿用）

- 独立数据库迁移只能向前演进；00001–00016 不可修改，新迁移从 00017 开始，并且必须对应一个经批准的修复。
- 不修改 Node 基线、既有 Crane 代码、DNS、TLS 和共享入口；`tools/task.mjs` 只能增加隔离的 baobei 分派。
- 验证入口：`node tools/task.mjs baobei <contract|lint|test|build|integration|e2e|security|compose>`。没跑的项目标 NOT_RUN，不得写 PASS。
- CI 以同一 SHA、同一 Run、Attempt 1 的 8/8 为准；失败不得靠重跑代替。
- 测试数据只用合成数据；提交信息用中文 Conventional Commits。
