# CRANE-SEC-001：npm 开发依赖安全例外（已批准，限时）

| 项 | 内容 |
|---|---|
| 编号 | CRANE-SEC-001 |
| 状态 | APPROVED（OWNER guolichen007，2026-11-18） |
| 责任人 | Crane 负责人（安全门禁责任人）；CC 负责实施与移除 |
| 公告 | GHSA-fc5q-39xc-vjpf / CVE-2026-112366（picomatch ReDoS，High，CVSS 8.7） |
| 范围 | 仅 picomatch@4.0.7，仅 package-lock 中 dev=true 的开发依赖；传播链 fdir、tinyglobby、vite、vitest、@vue/language-core、vue-tsc、@typescript-eslint/typescript-estree |
| 失效时间 | 2026-12-02T00:00:00Z（到期后门禁自动失败） |
| 机器可读记录 | `config/security/npm-audit-exceptions.json` |
| 门禁 | `tools/npm-audit-gate.mjs`、`tools/bundle-dev-deps-check.mjs`（CI security 作业） |

**这不是修复。** 漏洞仍然存在，上游尚无修复版本（最新版 4.0.7，公告发布于 2026-10-19）。CI 日志中的标记为 `NPM_AUDIT=PASS_WITH_APPROVED_EXCEPTION`。

## 为什么允许

- picomatch 只经由构建和测试工具进入依赖树，`npm audit --omit=dev --audit-level=high` 的结果为 0 HIGH / 0 CRITICAL。
- web 运行镜像只复制构建产物 `dist`，不复制 `node_modules`（见 `deploy/Dockerfile.web-production`）。
- 已用生产构建方式（`VITE_DATA_MODE=api`、`VITE_AUTH_MODE=oidc`）构建浏览器产物，逐个检查进入产物的模块：其中不含 picomatch、fdir、tinyglobby、vitest、@vue/language-core、vue-tsc。模块清单随 CI 证据一起归档。
- npm 给出的 “fix” 是把 vite 降一个主版本，会引入其他风险，因此不采用。

## 门禁如何判定（不按包名或数量放行）

1. 执行完整的 `npm audit --json`，原始报告保存为 `test-results-security/npm-audit-full.json`，作为 CI artifact 归档。
2. 单独执行 `npm audit --omit=dev --audit-level=high`。这一步必须通过，即生产依赖中不能有 HIGH 或 CRITICAL。
3. 逐条递归解释完整报告中的 HIGH 条目：
   - 直接公告只允许是本公告，且包名、受影响范围、严重度必须与记录一致。
   - 对 npm 的元漏洞（`via` 指向的其他包），要一直追溯到本公告为止。
   - HIGH 级别若只是由其他公告带来的，判为失败。
4. 每个被解释的包都必须在记录的传播链中；链上出现新包即视为依赖路径变化，判为失败。每个受影响节点在 lock 中都必须是 `dev=true`。picomatch 的版本必须是 4.0.7。
5. 以下任一情况都判为失败：任何 CRITICAL、例外过期或未批准、扫描失败、JSON 异常、报告中包含 error、出现未知依赖链。
6. Gitleaks、govulncheck、Trivy（文件系统和镜像）照常执行，门禁不变。

所有反证见 `tests/contract/npm-audit-gate.test.mjs`。

## 移除条件

上游发布修复版本后立即执行以下步骤：

1. 升级 picomatch，刷新 lock 文件。
2. 确认 `npm audit --audit-level=high` 在没有任何例外的情况下通过。
3. 删除 `config/security/npm-audit-exceptions.json`。门禁在没有例外文件时只接受零 HIGH；如需同时简化门禁实现，可另行提交。

如需延期，必须重新获得 OWNER 批准，并更新本文件和 JSON 记录中的到期时间。
