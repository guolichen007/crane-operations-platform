# Q4-QA-R1 全功能闭环反证与第一轮质量收口报告

| 项目 | 值 |
|---|---|
| 分支 | `feature/crane-qa-closure-r1`（自冻结基线 `905382bc78822a51cb2f4236b2e6b9510af7de89` 创建） |
| 基线 CI | Run `42947622323`，Attempt 1，9/9 SUCCESS |
| 本轮 HEAD | 见提交记录；CI 结果在推送后补记（§7） |
| 未改动 | main、`dev/crane-d1-rc1` 标签、迁移 00001–00012、服务器、Keycloak、Ingress、正式域名运行版本 |
| 未执行 | 未创建 RC2、未发布镜像、未部署 |

## 1. 结论摘要

| # | 缺陷 | 级别 | 状态 |
|---|---|---|---|
| D1 | 同一 SPA 内登录身份或项目授权变化后，首页项目列表、地图数据、全局搜索、资产台账仍显示上一身份的数据；上一身份的迟到响应还会覆盖新身份的台账 | **P0（已复现）** | 已修复 |
| D2 | Equipment 拆卸确认框只记住 ID，提交时从已刷新的列表读取版本；后台被他人修改后，会用新版本悄悄拆掉用户没见过的记录 | P1（已复现） | 已修复 |
| D3 | 创建位置的 API 没有业务校验：空名称、未知类型、只给一半坐标、WGS84 却无坐标、创建即停用都会返回 201 并写入；未知类型、未知或外项目父级、重复编号返回 503（"数据库不可用"） | P1（已复现） | 已修复 |
| D4 | 位置中心没有创建入口；资产"移动"在没有位置或位置加载失败（503）时，只显示一个空下拉框，没有任何说明；资产详情的"位置"页签是无功能按钮 | P1（已复现） | 已修复，位置页签已移除（DEFERRED，见 §6） |
| D5 | 设备目录在 503 时显示"暂无制造商 / 暂无型号目录"，且没有重试 | P2（已复现） | 已修复 |
| D6 | 冲突"覆盖"按钮对所有人显示，包括规划员和资源不允许覆盖的硬冲突（服务端会拒绝） | P2（已复现） | 已修复，仅调整 UI 显示，服务端授权未改 |

P0 已在本分支修复，修复前后都有浏览器证据，所以本轮不报 RELEASE_BLOCKED。不过 RC1（`905382b`）本身包含 D1，详见 §6。

## 2. 缺陷、源代码位置与修复

### D1 身份与授权隔离（P0）

- **根因**：
  - `apps/web-console/src/scene/provider.ts` 的 `apiProvider` 是全局单例，`loaded=true` 后不再重载；它的数据被首页（`Overview.vue` → `stores/operations.ts`）和 `components/GlobalSearch.vue` 共用。
  - `cloud/queryClient.ts` 的 vue-query 缓存按资源键（`['assets', …]`）存储，不包含身份。
  - `cloud/session.ts` 在 `/auth/session` 返回另一个用户或不同授权时，只替换 `webSession`，不通知任何数据层。
  - 已有的身份隔离只覆盖运营统计（`summaryIdentity`）。
- **修复**：
  - `session.ts`：新增 `sessionScopeKey`（userId + 排序后的 projectId:role）和 `scopeGeneration`。范围一旦变化（换账号、授权增减、角色变化、退出），派发 `cloud-identity-changed`；同一范围的空闲复核不派发。
  - `queryClient.ts`：收到该事件后调用 `resetQueries()`。它会取消进行中的请求、丢弃全部旧数据（不保留为"过期数据"），并按新身份重取。
  - `scene/provider.ts`：立即清空项目、设备、事件；每次加载记录 `scopeGeneration`，范围变化后的迟到结果不写入。
  - `useCloudQuery.ts`、`CloudDevices.vue`：同样清空数据并重载；LIVE 观看随之停止。
  - 运营统计原有的隔离逻辑没有改动。DEMO 模式没有登录身份，不触发这个事件。整个过程不需要刷新页面。

### D2 拆卸固定版本

- **位置**：`views/AssetDetail.vue`，原 `removeEquipmentMutation` 中的 `equipmentQuery.data.value!.find(...).version`。
- **修复**：
  - 打开确认框时固定 `{id, code, version}`（`removeTarget`），对话框显示"编号（基于 vN）"，提交时 `If-Match` 使用这个固定版本。
  - 提交中禁止重复点击，Esc、蒙层、取消都不能关闭对话框。
  - 收到 409 时保留对话框和错误信息，明确提示"未拆卸，请取消后重新打开"；重新打开会读取最新版本。

### D3 位置创建校验（后端）

- **位置**：`internal/asset/asset.go` 的 `CreateLocation`，以及 `services/cloud-api/internal/app/asset_api.go` 中的 `POST /projects/{id}/locations`。
- **修复**：新增 `validateNewLocation`，规则全部来自现有 `AssetLocation` 契约：
  - 名称必填，不超过 256 字；kind 必须是契约中的枚举值；长度限制按契约。
  - 新建位置只能是 ACTIVE。
  - 经纬度必须成对出现且在范围内；WGS84/GCJ02 必须带坐标；有坐标必须声明坐标系。
- **父级检查**：父级必须是本项目的 ACTIVE 位置，并加 FOR SHARE 锁。
- **错误映射**：输入错误返回 422；重复编号返回 409（预检加唯一约束兜底）；其余错误仍返回 503。
- **未改动**：迁移、契约、RBAC（`CapLocationWrite` 仍只给 platform_admin）都没有改。

### D4 位置最小闭环（前端）

- **`views/LocationCenter.vue`**：
  - 具备 `location_write` 的用户（与服务端一致，`canWriteProject`）可以看到"新建位置"，表单包含类型、名称、编号、上级（仅 ACTIVE）、地址。
  - 不提供坐标输入，提交固定 `coordinateSystem=UNKNOWN`，经纬度为 null，列表中显示"未知"。
  - 未选项目、未授权项目、加载中、错误（403/404/503 分别提示并可重试）、空列表（区分有无创建权限）、筛选为空，各自显示独立状态。
  - 草稿保护、提交中防重复，与资产编辑一致。
- **新增 `cloud/location-form.ts`**：表单的纯逻辑，有单元测试。
- **`views/AssetDetail.vue` 移动对话框**：
  - 加载中显示占位。
  - 加载失败显示错误（注明"不是没有位置"）并可重试。
  - 没有 ACTIVE 位置时明确提示，给出到"位置中心"（带当前项目）的链接，并禁用"移动"按钮。
  - 打开时重新拉取位置列表。
  - 移动返回 404 时提示"目标位置不存在或已停用"；成功后显示目标名称。
- **位置页签**：现有 API 不提供资产的当前位置（`asset_installations` 没有对外接口），所以直接移除，不编造当前位置。

### D5 设备目录

- **位置**：`views/CatalogCenter.vue`。
- **修复**：制造商和型号目录都分为加载中、错误（403/503/网络分别提示，带重试）、空、成功四种状态。

### D6 冲突覆盖按钮

- **位置**：`views/PortfolioWorkspace.vue` 的冲突表。服务端规则是 `POST /portfolio/conflicts/{id}/override`：`CapScheduleOverride` 只属于 portfolio_manager，并且资源 `overrideAllowed=false` 时返回 409。
- **问题**：冲突契约没有 scope 字段，前端无法判断某条冲突属于哪个项目组合。
- **修复**：
  - 只有调用者所属的所有项目组合都是 manager 时，才显示可执行按钮；否则显示"需项目组合经理覆盖"。
  - 资源不允许覆盖的冲突显示"硬冲突，不可覆盖"。
  - 服务端授权保持不变。
- **代价**：同时担任经理和规划员的用户暂时看不到按钮（偏保守，见 §6）。

## 3. 修复前后测试结果

修复前的结果是在提交前，用旧版源文件对同一组新测试运行得到的。

| 测试 | 修复前 | 修复后 | 证据（`docs/04-前端与可视化/evidence/q4-qa/`） |
|---|---|---|---|
| `premium/identity-isolation.spec.ts`（A→B、A→无授权、撤销授权、两类乱序，共 5 项） | 4 项失败：B 登录后首页仍列出 Alpha 项目；撤销授权后台账仍显示 Alpha 资产；A 的迟到台账响应覆盖了 B。地图乱序用例在旧代码上也通过，因为旧 provider 首次加载晚于身份切换，该用例的作用是防回归 | 5/5 通过 | before/after-user-b-overview、before/after-revoked-assets、before/after-out-of-order-assets 等 |
| `premium/equipment-remove.spec.ts` | 失败：旧代码发送 `If-Match: "4"`，记录被拆卸（`removedAt` 被写入） | 2/2 通过：发送 `"3"`，返回 409，记录未变；重新打开后按 `"4"` 成功 | before-equipment-remove、after-equipment-remove-409 |
| `app/location_create_http_test.go`（真实 PG17） | 失败：空名、半坐标、WGS84 无坐标、创建即停用、无坐标系等返回 201；未知类型、未知/外项目父级、重复编号返回 503；P001 写入 7 行（应为 2 行） | 通过 | 测试输出 |
| `premium/location-closure.spec.ts`（5 项） | 5/5 失败 | 5/5 通过 | before-move-empty、before-move-503、before-location-tab；after-location-1/2 |
| `premium/catalog-override.spec.ts`（4 项） | 3 项失败（目录 503 显示"暂无"；规划员看到覆盖按钮；硬冲突也有按钮） | 4/4 通过 | after-catalog-503、after-override-planner |
| `b4/asset-crud.spec.ts`（真实 Keycloak + PG） | — | 本地未运行（需要 Docker）：CI 的 b4 作业运行 | §7 |

## 4. 本地回归（HEAD 工作区）

| 项目 | 结果 |
|---|---|
| web-console vitest | 27 个文件，218 通过，另有 1 个是原有的 expected fail |
| vue-tsc typecheck、eslint | 0 错误 |
| 契约测试 `node --test tests/contract/*.test.mjs` | 63/63 |
| Go：`internal/asset`、`services/cloud-api/internal/app` 全包（真实 PG17） | ok |
| `go vet ./tests/integration/a1fixture/` | ok |
| Playwright premium（全部 15 个 spec 分批运行） | 全部通过，另有 1 项 skipped：原有的真实 GPU 验收用例 |
| Playwright cloud | 14/14 |
| Playwright demo（DEMO 不变） | overview 17、spatial 5、alarm 1、其余 13，全部通过 |
| b4 真实 Keycloak 套件 | 本地未运行（需要 Docker，仅在 CI 运行） |

## 5. 逐操作验收矩阵

格式说明："成功"和"失败"列中的条目引用测试文件和用例名称的关键词。p 表示 premium，b4 表示真实 Keycloak + PG，go 表示 Go HTTP/PG 测试，c 表示 cloud。前端只决定是否显示入口，服务端权限才是最终裁决。

| 按钮 / 操作 | 处理函数 → API | 服务端权限 | 成功路径 | 关键失败路径 |
|---|---|---|---|---|
| 账号登录 / 退出 | BrandLoginCover → `/auth/login`；TopCommandBar.logout → `POST /auth/logout` | OIDC + CSRF | auth/production：Code+PKCE、Cookie | b4/rbac："退出后旧会话不可继续写入"；p/premium："会话过期即卸载" |
| 顶栏项目范围 | TopCommandBar.changeScope → `?project=` | 服务端按授权过滤 | b4/nav：返回台账保留范围 | b4/nav："未授权项目范围显示明确提示"；p/location-closure：未授权项目 |
| 身份或授权变化 | session.setSession → `cloud-identity-changed` | — | **p/identity-isolation**（本轮新增） | **p/identity-isolation：撤销授权、乱序** |
| 全局搜索 Ctrl+K | GlobalSearch.choose → 深链 | 数据来自授权 Inventory | c/search-deeplink | **p/identity-isolation：换身份后搜不到 A** |
| 项目登记 新建 / 推进 | PortfolioWorkspace.submit/advance → `POST /portfolio/intakes`、`…/transition` | project_intake_write | p/portfolio-intake、b4/portfolio-intake | p/portfolio-intake：422/409/503、双击、草稿；无权限无入口 |
| 批准 | advance(APPROVED) → `…/approve` | project_approve（仅 manager） | b4/portfolio-intake | p/portfolio-intake：规划者无按钮，强制调用返回 403 |
| 成员授权 授予 / 调整 / 撤销 | FormalProjectPanel → `POST /portfolio/projects/{id}/members`（同一端点，role 为空即撤销；R1a 更正：此前误写为 PUT/DELETE） | project_grant_manage | p/portfolio-grants、b4/portfolio-grants | p/portfolio-grants：422/409/503；撤销后旧会话失效（b4） |
| 运营信息登记 | PendingInventoryProjects.submit → `POST /projects/{id}/inventory-registration` | location_write | p/inventory-registration | p/inventory-registration：503/403/409/超时/401/撤销/响应丢失 |
| 冲突覆盖 | PortfolioWorkspace.override → `POST …/conflicts/{id}/override` | schedule_override；硬冲突返回 409 | **p/catalog-override：经理一次请求** | **p/catalog-override：规划员、混合角色、硬冲突无按钮**；go/portfolio_security_test |
| 资产 新建 | AssetCenter.createMutation → `POST /assets`（幂等键） | asset_master_write | b4/asset-crud | p/asset-guards：草稿保护；b4/rbac：越权写入 P002 |
| 资产 筛选 / 分页 | AssetCenter.apply → `GET /assets?…` | read | b4/nav、p/command-scope | b4/nav：未授权范围 |
| 资产 编辑 | AssetDetail.updateMutation → `PATCH /assets/{id}`（If-Match） | asset_master_write | b4/asset-crud | b4/asset-crud：固定版本 409、重基、提交中 503 |
| 资产 移动 | AssetDetail.moveMutation → `POST /assets/{id}/move`（If-Match） | asset_master_write | **b4/asset-crud：UI 新建位置后移动**；p/location-closure | **p/location-closure：无位置引导、503/403 不显示空** |
| 资产 退役 | AssetDetail.retireMutation → `POST /assets/{id}/retire` | asset_retire | b4/asset-crud | 终态后无编辑入口（b4/asset-crud）；go/terminal_guard |
| Equipment 安装 | createEquipmentMutation → `POST /assets/{id}/equipment`（幂等键） | asset_master_write | b4/asset-crud | p/asset-guards：422、提交中不可关 |
| Equipment 编辑 | updateEquipmentMutation → `PATCH /equipment/{id}` | 同上 | b4/asset-crud | go/asset_patch：409 |
| Equipment 拆卸 | **openRemove → removeEquipmentMutation → `POST /equipment/{id}/remove`（固定 If-Match）** | 同上 | b4/asset-crud；p/equipment-remove | **p/equipment-remove、b4/asset-crud：固定版本返回 409、双击、提交中不可关** |
| 位置 新建 | **LocationCenter.submit → `POST /projects/{id}/locations`** | location_write | **p/location-closure、b4/asset-crud、go/location_create** | **422/409（重复编号）/403（viewer）、外项目父级；go 与 p** |
| 位置 查询 | LocationCenter → `GET /projects/{id}/locations` | read | p/location-closure | **p/location-closure：503/403、未选项目、未授权** |
| 设备目录 | CatalogCenter → `GET /manufacturers`、`/equipment/catalog` | read | p/catalog-override | **p/catalog-override：503 + 重试** |
| 全国地图导航、真实/模拟标识 | Overview / SpatialStage → summary + Inventory | read | p/homepage-h1-*、h2-1d、c/search-deeplink | p/h0-defects：未知区域；h2-1d：版本不一致、乱序 |
| LIVE 查看 / 停止 | CloudDevices → `POST/DELETE /devices/{id}/live` | watch | c/runtime：重连、停止 | c/runtime：pagehide keepalive；auth/production：跨项目被拒；**身份变化时停止**（本轮） |
| 告警刷新 | AlarmCenter → `GET /alarms`（15 s） | read | c/runtime：Cloud Alarm | c/runtime："API failure displays error" |

## 6. 未复现的风险与遗留（不影响首版开发 UAT）

- **NOT_REPRODUCED**：在 premium 模拟环境中，地图 Inventory 的乱序反证在旧代码上也通过（旧 provider 首次加载晚于身份切换）。为防回归，仍保留该用例和 `scopeGeneration` 守卫。
- **RC1 含 D1**：`dev/crane-d1-rc1`（`905382b`）未包含本修复。如果按 RC1 做开发 UAT，测试人员在同一浏览器标签页内换账号前应先刷新页面（退出后重新登录会整页跳转，本身不受影响）。修复需要新候选（RC2），本轮按要求未创建。
- **DEFERRED（需要新 API 契约）**：
  - 资产当前位置和位置历史：需要只读接口暴露 `asset_installations`，位置页签暂时隐藏。
  - 冲突覆盖按冲突精确授权：需要冲突契约增加 `portfolioScopeId`。目前混合角色用户暂时看不到按钮，可改用纯经理账号操作。
  - 位置编辑和停用 UI：API 已有，按本轮范围未做。
- **沿用 P1**：项目组合只有一名经理时的首次授权问题；生产库"从备份恢复"手册（D1 报告 §7.2）。

## 7. 提交与 CI

提交列表（基于 `905382b`）：`36d94fc`、`3e5fcbc`、`d0b862e`、`54ca316`、`16b3b42`、`2f8d609`、`747fcf6`，加上本报告的提交。

推送后补记：完整 HEAD、Run ID、Attempt、九项作业结果。

## 8. 结论

结论在 CI 九项通过后给出：ACCEPT_FOR_DEV_UAT 或 BLOCKED。

---

## 9. Q4-QA-R1a：安全门禁与跨项目误写的最小收口

**基线**：`21425c5`。CI Run `43143593507` Attempt 1 中，security 作业失败，原因是 govulncheck 报 GO-2026-6859；其余 8 项均成功，b4 也成功。

### 9.1 Go 安全漏洞

- **漏洞**：GO-2026-6859 / CVE-2026-97054，影响标准库 `net/http`（MaxBytesReader 并发设置 `Connection: close` 可能导致 panic）。受影响版本是 go1.27.0 至 go1.27.4 之前，修复版本是 go1.27.4。来源：pkg.go.dev/vuln/GO-2026-6859。
- **修改的文件**：
  - `go.mod`：`go 1.27.4`。
  - `.github/workflows/ci.yml`：9 处 `go-version: '1.27.4'`。
  - `deploy/Dockerfile.backend` 与 `deploy/production/images.json`：改为 `golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf`。
- **digest 来源**：从 Docker Hub 官方 `library/golang` 读取 `1.27.4-alpine` 的 OCI index digest；其 amd64 镜像配置中的 `GOLANG_VERSION=1.27.4`。digest 没有手写或编造。
- **未改动的部分**：所有安全扫描步骤都没有改动，也没有添加任何忽略规则。
- **本地验证**：
  - 用官方 `go1.27.4.linux-amd64.tar.gz`（sha256 `7226736c…4840`，与 go.dev 公布值一致），`go version` 输出 `go1.27.4`。
  - 在干净 worktree 中运行 govulncheck v1.9.0：
    - 修复前（go1.27.3）：报告 `GO-2026-6859 Found in net/http@go1.27.3, Fixed in net/http@go1.27.4`。
    - 修复后（go1.27.4）：`No vulnerabilities found.`
  - `npm audit --audit-level=high` 通过（只有 8 个 moderate）。
- **只在 CI 中运行的扫描**：gitleaks 本地下载依赖超时，未运行；Trivy 文件系统扫描和镜像扫描需要 Docker。这三项由 CI 的 security 作业执行，结果见 §9.6。

### 9.2 资产详情同组件切换导致误写（P0，已复现并修复）

- **复现**：打开资产 A 的编辑表单并填写草稿，然后只改路由参数到 `/assets/B`（组件复用，不卸载）。表单仍然存在，点击保存后实际发出的是 `PATCH /api/v1/assets/B`，`If-Match: "3"`，请求体为 `{"displayName":"A 的草稿名称"}`。A 和 B 的版本号相同时，服务端会接受这次写入。移动和退役对话框同样会写入 B。截图见 `evidence/q4-qa/before-asset-route-switch-edit.png`。
- **位置**：`views/AssetDetail.vue`。所有写操作都使用实时的 `id.value`（`route.params.id`），而且只有 `onBeforeRouteLeave`，没有 `onBeforeRouteUpdate`。
- **修复**：
  - 编辑、移动、退役、Equipment 安装在打开对话框时就固定资产 ID（`editTarget`、`moveTarget`、`retireTarget`、`equipmentAddTarget`），提交时只发往这个 ID；版本号在打开时就已固定。
  - 新增 `onBeforeRouteUpdate`：提交中直接拒绝切换；有草稿时需要用户确认放弃；切换后关闭上一资产的所有对话框。
  - 后端 RBAC 和 If-Match 都没有改动。
- **测试**：`premium/asset-route-switch.spec.ts`（编辑草稿、移动、退役、Equipment 安装、提交中切换、干净状态切换，共 6 项）。修复前 5 项失败，修复后 6/6 通过。

### 9.3 位置表单跨项目误写（已复现并修复）

- **复现**：同一个管理员同时拥有甲、乙两个项目的 `location_write`。在 `?project=PA` 下打开新建表单并填写，然后切换到 `?project=PB`。表单仍然存在，提交时请求 `POST /projects/PB/locations`，请求体是甲的草稿（名称"甲一车间"，parentId 为 A-SITE）。这次写入被拒绝，只是因为服务端检查发现父位置不属于乙项目；如果没有填父位置，就会直接在乙项目下创建成功。截图见 `evidence/q4-qa/before-location-project-switch.png`。
- **修复**（`views/LocationCenter.vue`）：
  - 打开表单时固定 `formProject`，提交到这个项目；上级位置只从这个项目里选，标题也显示这个项目。
  - `onBeforeRouteUpdate`：提交中拒绝切换项目；有草稿时需要确认放弃；切换后关闭表单。
  - 服务端仍然以 URL 路径中的项目、`location_write` 和父位置同项目检查为准。
- **测试**：`premium/location-project-switch.spec.ts`，3 项，修复前全部失败，修复后 3/3 通过。

### 9.4 项目组合授权（PortfolioGrant）撤销与降级（已复现并修复）

- **原因**：WebSession 的 `projects` 记录的是 ProjectGrant，不包含 PortfolioGrant，所以项目组合授权被撤销或降级时，前端不会收到身份变化事件。
- **复现**：
  - 撤销后再执行写操作，得到 403，但登记列表和新建入口仍然显示。
  - 从经理降级为规划员后，"批准"按钮仍然存在。
  - 离开页面再回来，仍然显示缓存中的甲组合数据（30 秒的 staleTime 内不会重新请求）。
- **修复**：
  - 新增 `cloud/queryClient.ts` 的 `resetPortfolioScope()`，重置所有项目组合相关缓存（scopes、intakes、resources、schedule-items、conflicts、portfolio-projects、project-members）。重置时直接丢弃旧数据，不作为过期数据保留，并按当前授权范围重新读取。
  - 两个时机会触发重置：进入项目组合页面时；任何项目组合写操作返回 403 时（登记新建与推进、批准、覆盖、成员授权）。
  - 没有改动认证系统。
- **测试**：`premium/portfolio-grant-revoke.spec.ts`，3 项，修复前全部失败，修复后 3/3 通过。

### 9.5 报告更正

成员授权使用的是 `POST /portfolio/projects/{id}/members`：同一个端点，`role: null` 表示撤销。原 §5 中误写为 `PUT/DELETE`，已更正。

### 9.6 本地回归（R1a HEAD）

| 项目 | 结果 |
|---|---|
| vitest | 218 通过，另有 1 个原有的 expected fail |
| vue-tsc、eslint | 0 错误 |
| 契约测试 | 63/63 |
| Go（go1.27.4，真实 PG17） | `./internal/...` 与 `services/cloud-api/internal/app` 全部 ok；`go vet` ok |
| premium | 全部 18 个 spec 通过，另有 1 项 skipped：原有的真实 GPU 验收用例 |
| cloud | 14/14 |
| DEMO | overview 17、spatial 5、其余 14 |
| govulncheck（go1.27.4） | No vulnerabilities found |
| npm audit high | 通过 |
| gitleaks、Trivy、b4 真实 Keycloak + PG | 本地未运行，由 CI 执行 |

CI 结果和最终结论在推送后补记。
