# R6-BIZ-R1 核心业务闭环缺口修复报告（F-02 设备类型 / 停用类型校验 / F-03 资产位置）

- 分支：`feature/crane-r6-biz-closure`，起点 `9809ecf44a8c942a20eb013de5b0b35d6216b14f`
- 不修改 R5 已冻结 Tag，不合并 main，不部署服务器，不修改三个 R5-UAT 项目，不自动推送 Tag
- 数据库 Schema：**仍为 12**，本轮没有新增迁移
- 远端 CI：**待 OWNER 推送后核验**（本报告 §9 留空，CI 回来前不得称远端 PASS）

## 1. F-02 根因（F02_ROOT_CAUSE）

原始证据：`evidence/r6-biz-r1/F02-root-cause.txt`（在基线 SHA 上用 `git grep` 读取）。

1. **代码注册表与数据库表的关系**：`internal/equipment/types.go` 是代码拥有的 43 个类型（同时决定规格 Schema）；`equipment_types` 是 Catalog 和安装记录的外键目标。`GET /api/v1/equipment/types` 返回两者的交集（注册表 ∩ `status='ACTIVE'` 的行）。任何一边缺失，该类型就不可安装。
2. **12 个迁移都没有初始化数据**：00005 只 `CREATE TABLE equipment_types`；全部迁移中的 `INSERT` 只在 00001（tenants/projects/users/roles），没有一条写 `equipment_types`。
3. **真实 UAT 空表场景**：全新部署迁移到 12 后该表为空 → 字典接口返回 `[]` → 安装表单没有可选类型 → 直接 API 安装因外键/查询失败而被拒（旧实现返回 400 加内部错误文本）。本轮的 `TestFreshSchema12BootstrapMakesEquipmentInstallable` 在全新 schema12 库上复现了这一状态（断言 0 行、安装被拒），之后用初始化命令使之可安装。
4. **B4 没有覆盖空表**：基线 `tests/integration/a1fixture/main.go:249` 在 seed 阶段直接 `INSERT` MOTOR/PLC；所有 Go 测试也各自插入类型。因此 CI 从未运行过"空目录"的部署形态。

结论：F-02 不是代码 Bug，而是**缺少受控的类型目录初始化步骤**，测试夹具替生产补了这一步，掩盖了问题。

## 2. A｜受控类型初始化（EQUIPMENT_BOOTSTRAP）

在现有模型下可以安全实现，**不需要迁移**。

- 库：`internal/equipmenttypes`（Plan / Apply / Verify / Scope）
- 命令：`tools/equipment-types`，已打包进 cloud-api 镜像（`deploy/Dockerfile.backend`、`tools/build-production.mjs`）
- 操作手册：`docs/08-部署运维与成本/R6-Equipment设备类型目录初始化操作手册.md`

| 要求 | 实现 |
|---|---|
| plan 只读 | 只读事务；输出 TO_CREATE / EXISTING / DISABLED / CONFLICT / UNREGISTERED 和 `digest` |
| apply 经单独授权、单事务 | 必须 `--approve <plan digest>` + `--actor`；事务内 `LOCK TABLE equipment_types IN SHARE ROW EXCLUSIVE MODE` 后重新分类，digest 不一致拒绝 |
| verify 一致性 | 无缺失、无冲突、无 UNREGISTERED 时退出码 0，否则 3 |
| 可重复执行 | 第二次 apply 为 `noop`，不新增任何行 |
| DISABLED 不被静默启用 | 已存在的行一律不 UPDATE；测试断言 DISABLED 行 status/version 不变 |
| 不接受任意客户端代码 | 范围只能来自注册表，未知代码在连接数据库前拒绝；HTTP 上没有类型写路由（测试断言 POST/PUT/PATCH/DELETE `/equipment/types` 为 404/405） |
| 不改变设备实例和历史 | 测试比对 apply 前后 `asset_equipment_installations` 与 `entity_revisions` 快照完全一致 |
| 不输出数据库密钥 | 密码仅从 0600 私有文件读取；输出与错误信息不含连接串（CLI 测试断言） |
| 审计 / 操作记录 | 每次 apply 写一条 `audit_logs`（`host:<actor>` / `equipment_types_initialized` / success 或 noop / request_id）；apply 的 JSON 输出即完整操作记录 |

前端：类型字典成功加载且为空时，安装表单显示「尚未配置可安装的设备类型，请联系管理员初始化类型目录」，类型未选或字典未成功加载时安装按钮禁用，提交前再校验所选类型属于已加载字典；没有新增任何创建全局类型的页面。字典缓存由 `Infinity` 改为 60 秒，打开安装表单时重新拉取，避免管理员初始化后页面仍显示旧的空列表。

## 3. B｜停用类型校验（EQUIPMENT_DISABLED_GUARD）

**反证成立**：基线 `resolveSpecSchemaVersion` 只读 `equipment_types.spec_schema_version`，不看 `status`，也不加锁；通过 Catalog 安装时完全不读 `equipment_types`。停用类型可以通过直接 API 安装。

旧实现运行新测试（`evidence/r6-biz-r1/B-before.txt`）：

| 测试 | 旧实现结果 |
|---|---|
| TestCreateEquipmentRejectsDisabledType | FAIL：停用类型安装成功 |
| TestCreateEquipmentRejectsDisabledTypeThroughActiveCatalog | FAIL：ACTIVE 目录项绕过停用类型 |
| TestCreateEquipmentRejectsUnregisteredOrMissingType | FAIL：返回未分类 `sql: no rows` |
| TestCreateEquipmentCatalogGuards | FAIL：类型不一致/停用/未知目录为未分类错误 |
| TestConcurrentTypeDisableBlocksInstall | FAIL：并发停用未提交时安装按旧快照成功 |

修复后同一组测试全部 PASS（`B-after.txt`），原有 `TestCreateEquipmentCatalogTypeMismatchRejected`、`TestUpdateEquipment*` 不变通过。

修复内容（`internal/asset/asset.go`）：typeCode 必须在注册表中；`equipment_types` 行必须存在且 ACTIVE；Catalog 必须存在、ACTIVE、类型一致、全局或同项目；两行均 `FOR SHARE` 读取，并发停用要么先提交被看到，要么等安装提交。已安装设备不受影响，测试断言停用后旧安装仍可编辑、拆卸，修订记录 3 条完整。

HTTP 错误分类（原来全部 400，包括数据库故障）：不可安装/输入错误 422，跨项目目录 422，资产已退役 409，幂等冲突 409，资产不存在 404，数据库故障 503。重复的 Equipment 编号、非法关键度由约束错误转为 422。

## 4. C｜当前位置与安装历史（F03_LOCATION_READBACK / LOCATION_HISTORY）

新增 API（只读，均为项目范围优先，`Cache-Control: no-store`）：

| 路由 | 返回 |
|---|---|
| `GET /api/v1/assets/{id}/location` | `AssetLocationReadback { assetId, projectId, assetStatus, current }`；`current` 为 null 或 `{ installationId, locationId, code, name, kind, status, validFrom, path[] }` |
| `GET /api/v1/assets/{id}/installation-history?limit=1..100&offset=` | `AssetInstallationHistory { assetId, total, limit, offset, items[] }`，`items` 按 `valid_from DESC, id DESC`，每条含 `validTo`、`current`、`path[]` |

实现要点：当前记录以 `asset_installations.valid_to IS NULL` 判定（唯一索引保证至多一条）；完整路径用递归 CTE 自根到叶读取，限定同项目，深度上限 16 防环；读取在 `READ ONLY REPEATABLE READ` 事务内，资产、安装和路径来自同一快照；不返回任何经纬度字段。`MoveAsset` 未改动。已退役资产照常可读。

安全语义：资产不存在或不在调用者项目内一律 404（与 `GET /assets/{id}` 一致，并写 `authorization_denied` 审计）；未登录 401；同项目 viewer 可读；两条路由均无写方法。

前端（`AssetDetail.vue`）：概览页新增「当前位置」区：加载中骨架、读取失败显示错误与重试（明确不是"未安装"）、无记录显示「该资产尚未安装到任何位置」、有记录显示位置名称（链接到位置中心）、编号、类型、位置状态、安装起始时间和完整路径。「查看位置历史」打开分页弹窗。移动成功后刷新当前位置与历史，提示中提供「在位置中心查看」。位置中心支持 `?location=` 高亮对应行（仅在已授权项目的列表内查找，不扩大读取范围）。页面注明不显示经纬度、不以行政区中心代替现场位置。

截图：`evidence/r6-biz-r1/shots/f03-empty.png`、`f03-current.png`、`f03-history.png`、`f02-empty-types.png`。

## 5. D｜测试清单

| # | 要求 | 测试 | 层 |
|---|---|---|---|
| 1 | 全新 schema12 初始化到可安装 | `TestFreshSchema12BootstrapMakesEquipmentInstallable`；`TestR6EquipmentTypeDirectoryHTTP` 步骤 1–2；B4 `@r6-empty-types` + 镜像内 `equipment-types` plan/apply/verify | PG / HTTP / 真实浏览器 |
| 2 | 重复执行 | `TestApplyIsRepeatableAndDigestBound`；B4 runner 第二次 apply 断言 `noop` | PG / CI |
| 3 | 冲突与 DISABLED 保护 | `TestDisabledConflictAndUnregisteredProtection`、`TestApplyDoesNotTouchInstallationsOrHistory` | PG |
| 4 | 不允许安装停用类型 | `TestCreateEquipmentRejectsDisabledType`、`…ThroughActiveCatalog`、`TestConcurrentTypeDisableBlocksInstall`；HTTP 步骤 4 | PG / HTTP |
| 5 | Catalog 停用、跨项目、类型不一致 | `TestCreateEquipmentCatalogGuards`；HTTP 停用类型经 ACTIVE 目录 422 | PG / HTTP |
| 6 | 首次无位置空态 | `TestAssetLocationReadbackAndHistory`；HTTP `current:null`；premium/B4 浏览器空态 | PG / HTTP / 浏览器 |
| 7 | 移动前后位置与历史 | 同上 + B4 两次界面移动、刷新、历史两条、跳转位置中心 | PG / HTTP / 浏览器 |
| 8 | 跨项目读取拒绝 | `TestR6AssetLocationReadbackHTTP`（P002 读 P001 → 404 无泄漏、审计）；B4 双向 404 | HTTP / 真实浏览器 |
| 9 | 并发移动与版本冲突 | `TestConcurrentMoveLeavesExactlyOneCurrentLocation`（5 轮，恰 1 胜、1 条当前）；HTTP 旧 If-Match 409 | PG / HTTP |
| 10 | 真实浏览器资产详情与 Equipment 闭环 | `tests/e2e/b4/r6-business-closure.spec.ts`（真实 Keycloak + PG）；`tests/e2e/premium/r6-asset-location.spec.ts`（模拟 API，4 用例） | 浏览器 |

全部新 PG/HTTP 测试已加入 `tools/r2-b2-test.mjs` 的 R6 门禁（SKIP 即失败），并由 `tests/contract/r2-db-gate.test.mjs` 锁定清单。

B4 夹具变更：`a1fixture` 不再预置类型；`tools/a1-production-test.mjs` 先只跑 `@r6-empty-types`（空目录），再用候选镜像中的 `equipment-types` 执行 plan → 批准 apply → verify → 再次 apply（noop），JSON 记录保存到 `test-results-r6/` 并随 CI 证据归档，然后运行其余 B4 用例。原 `asset-crud`、`visual`、`rbac` 等依赖 MOTOR 的用例在初始化后运行，行为不变。

浏览器定位：新用例均限定在页面区域（`.detail-actions`、`asset-detail .tabs`、具名对话框和 `data-testid`），不使用全页 `getByRole` 文本匹配，避免与侧栏条目混淆。

## 6. 本地验证（原始输出在 `evidence/r6-biz-r1/`）

| 项 | 结果 | 文件 |
|---|---|---|
| 停用类型反证：旧实现 | 5 FAIL（预期） | B-before.txt |
| 停用类型反证：修复后 | 8 PASS | B-after.txt |
| 类型目录库 | 4 PASS | A-directory.txt |
| CLI（真实 PG，不泄露密钥） | 2 PASS | A-cli.txt |
| 位置读取 / 并发移动 | 3 PASS | C-repo.txt |
| BFF HTTP（R6 两组） | 2 PASS | D-http.txt |
| `-race` R6 相关 | ok | go-race.txt |
| Go 回归：asset / equipmenttypes / equipment / equipment-types / migrations | ok | go-reg-1.txt |
| Go 回归：cloud-api app 全包 | ok | go-reg-app.txt |
| Go 回归：identity / portfolio / inventory / operations / webauth / uatdata | ok | go-reg-2.txt |
| Go 回归：scheduling / fleet / platform / geo / contracts / astguard / device / session / auth | ok | go-reg-3.txt |
| 前端 vitest | 30 文件，230 通过 + 1 预期失败 | vitest.txt |
| typecheck（含 e2e spec）/ lint | PASS | — |
| 契约测试（CI repository-contract 列表除 web-nginx） | 63/63 | contract.txt |
| codegen `contracts/openapi/geoindex --check`、actionlint、astguard、gofmt、go vet | PASS | — |
| Playwright premium：R6 新增 4 用例 | 4 passed | E2E-r6-premium.txt |
| Playwright premium 回归：asset-guards 6、route-switch 6、location-closure 5、r5-mobile-overflow 6、equipment-remove（提交中）1 | 全部通过 | E2E-reg-*.txt |

本地未运行（以 CI 为准，不计 PASS）：staticcheck（沙箱磁盘不足）、govulncheck/gitleaks/trivy、`web-nginx` 契约测试、B4 真实 Keycloak + Docker 浏览器套件、integration、完整 `npm run test:e2e`。本地 Playwright 使用的是沙箱内已有的 headless shell（Chrome 151，以 1248 目录名软链），CI 使用其固定版本。PostgreSQL 本地为 18.4，CI 夹具为 17.8。

## 7. 兼容性

- 契约新增 5 个 REST 类型（AssetLocationPathNode / AssetCurrentLocation / AssetLocationReadback / AssetInstallationRecord / AssetInstallationHistory）与 2 条 GET 路由，均为新增，不改变已有字段与路由。
- `POST /assets/{id}/equipment` 失败状态码由一律 400 细分为 422/409/404/503；成功路径不变。前端已有的「输入不符合要求」提示即对应 422。
- 安装校验更严格：此前可以安装的"停用类型"和"未初始化类型"现在被拒绝。这是本轮的修复目标；已有安装记录不受影响。
- 镜像新增 `/usr/local/bin/equipment-types`，入口点仍为 `service`。
- 部署顺序：发布本版本后必须按操作手册执行一次类型初始化，否则 Equipment 仍不可安装（这正是 F-02 现状，不会变差）。

## 8. 迁移结论（MIGRATION_REQUIRED）

不需要。初始化在现有 Schema 12 内通过受控命令完成，没有放松任何安全门禁。若将来要求"部署即自带类型目录"，可再评估用迁移写入，但那会把业务目录变更与 Schema 版本绑定，并影响 Down 治理与恢复演练，本轮不采用。

## 9. CI（待补）

| 字段 | 值 |
|---|---|
| 提交 SHA | 待 OWNER 推送 |
| CI Run | 待补 |
| 九项结论 | 待补 |
| b4-business-acceptance：`R6_EQUIPMENT_TYPES_BOOTSTRAP=PASS` 行 | 待补 |
| r2-db-test：`R2_R6_BIZ_DB_TEST=PASS` 行 | 待补 |

## 10. 封板状态

```
F02_ROOT_CAUSE                     = CONFIRMED（迁移无数据；夹具预置掩盖空表；见 §1）
EQUIPMENT_BOOTSTRAP_PASS           = LOCAL_PASS（PG/CLI/HTTP）；CI 待补
EQUIPMENT_DISABLED_GUARD_PASS      = LOCAL_PASS（旧实现 5 FAIL → 修复后 PASS）；CI 待补
F03_LOCATION_READBACK_PASS         = LOCAL_PASS；CI 待补
LOCATION_HISTORY_PASS              = LOCAL_PASS；CI 待补
CROSS_PROJECT_ISOLATION_PASS       = LOCAL_PASS（HTTP 404 + 审计）；B4 真实浏览器待 CI
EXISTING_BUSINESS_REGRESSION_PASS  = LOCAL_PASS（Go 全包 / vitest / 选定 premium）；B4 与完整 e2e 待 CI
MIGRATION_REQUIRED                 = NO（Schema 仍为 12）
CI_9_OF_9                          = PENDING（待 OWNER 推送）
OPEN_P0                            = 0
OPEN_BLOCKING_P1                   = 0（部署后必须执行一次类型初始化，已写入操作手册，属发布步骤而非缺陷）
```

封板判定须等同一完整 SHA 九项 CI SUCCESS 后由独立审查确认。
