# 项目报备中心工程入口

仅适用于 product/baobei-v1 独立 worktree。当前阶段 G4_S1B1_R0_DEPLOY_READINESS（部署前最终门禁，不部署；冻结候选 ca41c3b，仅上线必需修复），START_HEAD=ca41c3ba090a7dc17120f6565d0a54a2f8d644fd。G2 见20，G3-0 见24，G3-1 见25/26，G3-2A 见27，G3-2B 见28，G3-2C 见29，G3-3-P0 见30/30-R1，G3-3A 见31，G3-3B 见32，R1 见32-R1，G3-4-A0 见33，G3-4-A1 见34，G4-S0 见35，G4-S1A 见36，G4-S1B0 见37，G4-S1B1 执行清单见38；Preview 部署只按38号顺序手工执行，不得用 direct-deploy.py；APP_ENV=preview 必须与 production 同一加固路径，Preview 与生产的 origin/issuer/client、Compose 名称、网络、卷、secret 不得互用，改动需通过 tests/baobei/preview.mjs 与 production.mjs；E2E 提交到审核队列的数据必须用 tests/baobei/e2e/support/isolation.ts 的 cleanup fixture 先登记后提交，审核卡用 findCard 翻页定位；工作台只用 workspace-summary 与 stage-summary，每个数字须能点击进入同口径列表且 total 相等，不显示收入/回款/同比/赢率；向导内未纠正的无效数字不得随步骤切换被丢弃。只正常提交/push产品分支，等待同SHA原八门CI后STOP；不得自动进入G3-4其余功能或G4-S1，不得部署Preview或生产，不授权release ref或GHCR。
唯一事实源：docs/项目报备中心/00-项目总览与SSOT.md；UI 动作事实源：交互动作矩阵.md。
固定基线 29c6ec901f28e1fdc1c34b91f1d07e49de9a2b18；永久产品分支 product/baobei-v1。
禁止合并 main / R2，禁止修改既有 Crane 代码、生产服务器、DNS、TLS、真实账号及现场数据。
允许独立产品路径及专用 CI；tools/task.mjs 仅可增加隔离 baobei 分派。不得修改 Node 基线。
独立数据库 migration 从 00001 开始，forward-only。协议优先；synthetic 测试；中文 Conventional Commits。
只允许最终 git push origin product/baobei-v1；禁止其他 push / PR / tag / force。
验证入口 node tools/task.mjs baobei <contract|lint|test|build|integration|e2e|security|compose>。
先 contract、lint、test、build 后 Compose 集成；未执行标 NOT_RUN，不得宣称 PASS。
REA evidence 仅 local-private/research/baobei-ui/，禁止提交。禁止提交凭据、日志、生成物。
