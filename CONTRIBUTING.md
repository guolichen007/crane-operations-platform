# 贡献规范

从 main 建立 feature/*、fix/*、docs/*、chore/* 短期分支。提交格式 feat(scope): 中文摘要，每次单一目标，在正文记录验证。
契约变更先审查 contracts 的兼容性；迁移采用 forward-fix，禁止修改已应用迁移。公开样例只使用 Project-A、SIM-001。
提交前运行 node tools/task.mjs lint、test、build、integration-test；提交测试证据、影响和回滚步骤。
用户授权进入 PR 流程后使用模板，由 CODEOWNERS 审查，不自动 merge。当前 Phase 1-A0 按用户要求不创建 PR，最终提交必须完整 CI 成功且 CI_SHA 等于 HEAD_SHA。服务器部署须另行人工批准；生产接入须完成后续安全和现场验收。
禁止引入生成物、node_modules、数据库、生产日志、证书和私钥。根许可证未决定，保留第三方 notices。
