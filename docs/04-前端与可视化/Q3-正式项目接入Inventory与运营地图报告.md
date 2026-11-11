# Q3 正式项目接入 Inventory 与全国运营地图：报告

> 分支 `feature/crane-dev-single-origin`，基线 `e82c986`（开始时远端 HEAD 与之一致）。
> 本轮**没有新增迁移**，没有改 main、服务器、Keycloak、设备控制或 Three.js，也没有新增全局绕过或自动授权。

## 0. 上一 SHA 的 CI 证据（e82c986，run `42930334724`，attempt 1）

| 作业 | 结果 |
| --- | --- |
| edge / backend / r2-db-test / repository-contract / a1-security / integration / security | success |
| frontend | **success**：`test:e2e` 00:19:59–00:32:22，约 12.4 分钟，整个作业约 13.7 分钟，上限 20 分钟 |
| b4-business-acceptance | **failure**：34 passed、2 failed（见下） |

Q1 的 run `42812630444` 中，frontend 显示 `cancelled`。`test:e2e` 步骤从 12:11:52 跑到 12:24:33（12.7 分钟），作业从 12:04:21 到 12:24:38，约 20.3 分钟，达到 `timeout-minutes: 20`。主要原因是那次 `playwright install --with-deps` 用了 6 分钟（这次只用了 14 秒）。结论：这是一次环境慢导致的超时取消，不是测试失败。本轮**没有**修改 CI 时限，也没有删除测试。

b4 的两个失败都已定位，并在本提交修复：

1. `b4/portfolio-intake.spec.ts`：Q2 起规划者**不再显示**批准按钮，但这个 Q1 的 B4 用例仍去点击它，等待 180 秒后超时。这是我在 Q2 只更新了 premium 用例、漏改 B4 用例造成的。现在改为断言按钮不存在，并直接调用接口确认服务端返回 403（安全断言没有减弱）。
2. `b4/portfolio-grants.spec.ts` 反证用例：规划者页面在批准**之前**已打开，切到「正式项目」页签时，请求被 30 秒查询缓存挡住，新批准的项目不显示。**这是真实的产品缺陷。**修复：切到该页签时主动重新获取。新增 premium 回归用例「页面打开后才被批准的正式项目…」；去掉修复后，该用例在本地失败。

## 1. 数据流转

```
project_intakes ──ApproveIntake──▶ projects(id=FP, lifecycle NULL, portfolio_scope_id)
                                   │  Q2：项目组合经理授予 ProjectGrant（不自动）
                                   ▼
                     project_memberships(user, FP, platform_admin)
                                   │  Q3：该用户（location_write）登记运营信息
                                   ▼
   UPDATE projects SET lifecycle, province/city/county（由行政区索引派生）, admin_code,
                       location_label, schedule_text, notes   WHERE id=FP AND lifecycle IS NULL
                                   │  同一事务写审计 project_inventory_registered
                                   ▼
   同一 ID FP 出现在：/api/v1/projects（按 ProjectGrant 过滤）、/api/v1/assets/devices、
   /api/v1/operations/summary（按行政区归属）、首页 2D/3D 地图（行政区级）、资产台账 /assets?project=FP
```

- **只有一个实体。**`projects` 同时承载正式项目和 Inventory 项目，没有复制数据，ID 不变。
- **登记前后的判定。**`lifecycle IS NULL` 表示未登记；登记只能做一次，重复请求返回 409，不覆盖已有数据。
- **登记权限。**需要该项目的 `location_write`（目前只有项目 platform_admin 有）。项目组合权限不能代替，接口里根本不检查项目组合权限。
- **位置来源。**省、市、区县名称都由行政区索引派生，用户不能自填，所以层级和名称不会矛盾。不存储也不虚构坐标，地图按行政区中心定位，并标注精度。
- **运行源。**统计只来自 `devices` 和 Shadow。没有运行身份时，界面显示「尚未接入运行源」，REAL、SIM、TEST 都是 0。

## 2. 后端

| 接口 | 行为 |
| --- | --- |
| `GET /api/v1/inventory/pending-projects` | 调用者**有 ProjectGrant**的、已批准但未登记 Inventory 的正式项目，附带调用者角色、`canRegister`（是否有 location_write）和已登记资产数 |
| `POST /api/v1/projects/{id}/inventory-registration` | 请求体 `{lifecycle, adminCode, locationLabel, scheduleText, notes}`，严格解码，未知字段（如 province、longitude）返回 422。返回码：无授权、未批准、Inventory 项目、未知 → 404；有授权但无 location_write → 403；非法编码或国家级编码 → 422；已登记 → 409；行政区索引不可用 → 503（不写入、不猜测）。锁住项目行，更新和审计在同一事务里提交 |
| `POST /portfolio/projects/{id}/members`（**修复 A**） | 改为返回 `ProjectGrantResult {result:"APPLIED", members, membersRefreshed}`。授权一旦提交，重新读取成员列表失败也只会让 `membersRefreshed=false`，**绝不**返回错误；前端会单独重新加载列表 |

## 3. 前端

- 「项目」页（/projects）新增「待登记运营信息的正式项目」区：
  - 只有项目管理员能看到「登记运营信息」按钮，其他角色显示「需项目管理员登记」。
  - 表单字段：生命周期；省、市、区县三级下拉（数据来自地图使用的同一份 geo-manifest）；位置说明；实施计划备注。
  - 表单实时显示「定位精度：……（区县级行政区定位，非设备精确坐标）」。
  - 防重复提交；失败原位提示并保留输入；有草稿保护。
  - 行政区清单加载失败时，下拉框禁用并给出提示。
- 登记成功后刷新以下数据：待登记列表、正式项目、项目列表、首页 Inventory。
- 项目卡片：显示「尚未接入运行源」，新增「资产台账」链接。
- 首页设备面板：新增「查看资产台账」链接，从首页可以直接打开该资产详情。
- **修复 B**：授权表单有草稿时，切换页签、切换项目、关闭面板都需要确认；保存中直接拒绝切换，避免重复提交。

## 4. 真实 PostgreSQL / BFF（`inventory_registration_http_test.go`，均已纳入 r2-db-test 必过门禁）

| 要求 | 证据 | 结果 |
| --- | --- | --- |
| 审批前不能登记 | 未批准的项目返回 404 | PASS |
| 已批准但无 ProjectGrant 的经理不能登记 | 404；规划者、其他项目的 platform_admin、其他项目组合经理同样返回 404；viewer 授权返回 403 | PASS |
| 获得 platform_admin 授权后可以登记 | 200 | PASS |
| 登记前不在 Inventory，登记后出现同一 ID | `/api/v1/projects` 前后对比；未授权的 platform_admin 和经理看不到 | PASS |
| 资产数与 Inventory 一致 | 预先建的 2 个资产：登记响应中 assetDeviceCount=2，`/assets/devices` 中 FP-A 有 2 条，台账中 2 条，区县 summary 中 registered=2 | PASS |
| 跨项目、跨项目组合、非法行政区被拒绝 | FP-B、P001（Inventory）返回 404；999998 / 100000 / 非法生命周期 / body 带 province 或 longitude / 超长 返回 422；前后 `lifecycle` 计数和审计都不变 | PASS |
| 同时提交、重复提交、失败回滚 | 6 个并发请求恰好 1 个返回 200、5 个返回 409，只有 1 条审计；重复提交返回 409 且数据不被覆盖；行政区索引故障返回 503 且未写入 | PASS |
| 审计完整 | `project_inventory_registered`，actor、project、request_id 和 `success:PLANNED:330481` 都正确；被拒绝的请求没有成功审计 | PASS |
| 没有设备时不伪造 REAL/SIM/TEST | runtimeDeviceCount、realConnectedCount、simulatedCount 都是 0；summary 中 real.connected=0，bound 合计 0，noRuntime=2 | PASS |
| 登记不授权 | 登记前后成员数不变 | PASS |
| 修复 A | 注入「成员读取失败」后：返回 200 APPLIED 且 `membersRefreshed=false`，授权已写入；客户端重试返回 409，审计只有 1 条 | PASS |

变异验证（逐个改坏后测试都失败，验证完已复原）：

1. 能力从 location_write 改为 read。
2. 去掉「已登记」检查和 `lifecycle IS NULL` 条件。
3. 去掉「已批准登记」的关联条件。
4. 成员读取失败改回返回 503。
5. 去掉切换页签时的草稿保护（前端修复 B）。
6. 去掉切换页签时的重新获取（CI 发现的问题）。

## 5. 浏览器

| 套件 | 内容 | 结果 |
| --- | --- | --- |
| `premium/inventory-registration.spec.ts`（模拟 API，按服务端规则响应） | 项目管理员：待登记 → 选择省、市、区县（显示精度）→ 登记 → 项目卡片同一 ID，显示「尚未接入运行源」、资产 2 → 首页区县 KPI 登记 2、REAL 在线 0、无运行身份 2 → 设备面板显示「未接入」「行政区中心示意」→ 资产台账详情；viewer 不能登记；其他账号看不到；待登记 503、行政区清单失败、登记 503 后保留输入并可重试；WebGL 不可用时回退二维；三档分辨率 | 7/7 |
| `premium/portfolio-grants.spec.ts` | 新增：修复 A、修复 B、CI 发现的缓存问题 | 10/10 |
| `b4/portfolio-grants.spec.ts`（**真实 Keycloak + PG**） | 新增：经理（无项目权限）登记返回 404 → 被授权人在项目页登记 → 卡片显示行政区级定位和「尚未接入运行源」→ `/projects` 中 assetDeviceCount=1、realConnectedCount=0、adminCode=330481 → summary 中 REAL 在线 0 | **NOT_RUN（本地没有 Docker）**，看同 SHA 的 CI |

截图（`docs/04-前端与可视化/evidence/q3-inventory/`）：

- `1-registration-form`、`2-projects-list`、`3-homepage-county`、`4-device-panel`、`5-register-503`
- 分辨率：`6-form-1280x720`、`6-form-1366x768`、`6-form-1920x1080`

具体操作步骤（项目管理员）：

1. 打开「项目」，找到「待登记运营信息的正式项目」，点击「登记运营信息」。
2. 选择生命周期，再选择省、市、区县，确认「定位精度」一行，然后保存。
3. 项目卡片出现。点「沙盘定位」进入首页对应位置；点「资产台账」进入项目资产。
4. 在首页项目面板中点资产 → 设备面板 → 点「查看资产台账」进入资产详情。

## 6. 回归

- Go：`internal/{inventory,identity,portfolio,operations}`、`contracts`，以及 `services/cloud-api/internal/app` 全包通过；vet、gofmt、codegen `--check` 通过。
- 前端：vitest 26 个文件、213 项通过；typecheck 和 lint 通过。
- 浏览器 premium 全套件：34 + 39 + 12 + 21，全部通过，含 1 项既有 skip。
- 契约测试 63/63。

## 7. 遗留问题

| 级别 | 问题 |
| --- | --- |
| P0 | 无 |
| P1 | 真实 Keycloak 下的 Q3 浏览器步骤和 e82c986 那 2 个 b4 失败的修复，都要等新 SHA 的 CI 确认 |
| P1 | 项目组合只有一名经理时的首次授权问题保持不变：经理不能给自己授权（不悄悄放开） |
| P1 | Inventory 登记只能做一次；登记后如需更正行政区或生命周期，目前**没有**修改入口，只能由宿主机处理 |
| P2 | 首页由 sceneProvider 加载的数据：只有本页登记成功时才会刷新；其他用户的登记需要刷新页面才能看到 |
| P2 | CI 的 frontend 作业时间接近上限（正常约 14 分钟，环境慢时会超过 20 分钟）。是否调整时限请决定，本轮不改 |
| P2 | 正式项目、待登记和成员列表都没有分页 |
