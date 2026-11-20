# R9-UX 全平台中文化与产品体验最终收口：验收报告

- 基线：`0d6e86205b46c4dc4a17fd9cfe9f2c3ab1dec38b`（CI 44662190217 9/9，Schema 16；G1/G2 已通过现场验收，57 项模拟计量运行中）
- 代码提交：`85cb0447832260b322348a791348286f550c39a8`（分支 `feature/crane-r9-lhk-first-asset`）
- 范围：只改展示层与登录文案。**Schema 保持 16，无迁移。** 后端只加了两个向后兼容的 JSON 字段。数据库枚举、API 取值、MQTT 协议、原始 Excel 和用户输入的原文都不改。
- 部署：源码直部署，不发布镜像。只涉及 `web`、`cloud-api`（加法字段 + `ui_locales`），以及 Keycloak 主题文案（主题为只读挂载，重启 keycloak 容器即可生效，**不重新初始化 Realm**）。不清空业务数据，不重新导入老河口，不跑 60 分钟容量测试。

本报告区分「本地已验证」「CI 待核」「服务器待执行」，未执行的项不写 PASS。

## 1. 统一中文展示模块

`apps/web-console/src/cloud/zh.ts` 是全站唯一的业务枚举 → 中文来源，覆盖以下内容：

- **设备与角色**：51 种器件类型（与 `internal/equipment/types.go` 逐项一致，vitest 校验）；系统、项目组合、项目三级角色。
- **状态**：资产、位置、目录、项目生命周期、关键度、管理状态；运行模式、连接、采集、实时查看、健康、断开原因。
- **点位与计量**：点位的原始类型和派生类型、采集来源、单位（`count`→次、`h`→小时、`t`→吨）、有效电平与边沿、质量、计量种类与范围、可信状态、异常类型。
- **维护**：规则种类与类型、审批状态、排程状态、预警阶段（`LEAD_7`→提前 7 天提醒、`PCT_100`→使用量 100%）、预警状态，工单的状态、类型、结果与优先级，数据缺口，钢丝绳检查结论。
- **核查与历史**：核查项、待确认属性及其取值；历史类别、实体与字段；台账、工单、钢丝绳、故障摘要的结构化映射（只改显示，不改审计原文）。

**未知值**一律显示中文“未知 / 状态未知 / 未登记的设备类型”。原始编码只在技术详情里出现，例如「未登记的设备类型（编码 X）」。所有 `data-testid` 和 `data-*` 属性仍保留原始枚举值。

**错误**由 `errorZh` 和 `validationZh` 处理：

- 按 `ApiError.code`、HTTP 401/403/404/409/422/428/429/5xx 以及断网和超时，映射为中文。
- 英文诊断只出现在「详情」里（`errorDetail`），不作为主信息。
- 写入结果未知（`NetworkWriteError`）保持原语义，409 之后的回读保留，403 的权限隔离文案保留。

## 2. 逐路由中文化清单

「守卫」指 `tests/e2e/premium/zh-guard.ts` 的可见文本检查，它会拒绝四类问题：枚举泄漏、英文公式、双语装饰、未翻译错误。「视口」指 `r9-ux-localization.spec.ts` 在 1280、1440、1920、375、390 五种宽度下逐路由检查，并断言无页面级横向溢出。

| 路由 | 主要处理 | 守卫覆盖 |
|---|---|---|
| 登录封面 / Keycloak 登录 / 首次改密 | 封面去除全部英文；主题补齐 zh_CN 文案（设置新密码、新密码、确认新密码、常见错误）；BFF 跳转带 `ui_locales=zh-CN` | Go 单测；b4 真实 Keycloak 断言（CI 待核） |
| 顶栏 / 侧栏 | 产品名「友道智造 · 行车综合管理平台」；「运营工作台」「版本 A1.1」；退出失败提示中文 | 视口 |
| `#/overview` 运营总览 | 取样时间悬浮提示去掉 `sampledAt` / `generatedAt` / `GET` 路径；数据来源改为「登记 / 演示遥测」等；「真实在线」 | 视口 + homepage 回归 |
| `#/runtime`、`#/devices/:id` 运行状态 | 健康、采集、断开原因中文化；标题改为「最近状态」「实时查看：实时采集中」；静默说明不再出现 LIVE | 视口 + cloud/integration（CI 待核） |
| `#/projects` 项目 | 去掉「ASSET SITE」，改为「项目编号 …」；待登记项目中的角色显示中文 | 视口 |
| `#/devices` 登记设备 | 「尚未接入云端」；KPI 钻取说明改为「真实在线 / 当前真实告警」 | 视口 + homepage-h2 |
| `#/alarms` 告警 | 「云端上报」「上报时间（北京时间）」 | 视口 |
| `#/assets` 资产中心 | 类型选项中文（值不变）；加载和创建错误中文 | 视口 + 403/404/5xx/断网正向断言 |
| `#/assets/:id` 资产详情 | 「器件 / 安装器件 / 器件编号 / 运行身份绑定」；资产类型中文；旧类型显示「旧类型（编码 X，保持不变）」；冲突值与位置类型中文；器件类型显示「中文名（编码）」 | 视口 + 错误态 + r9-maintenance |
| 资产详情 · 台账与点位 | 主表：器件、品牌型号、计量方式、现场确认；编号放在次列；PLC 地址、原始类型、寿命原文、「第 N 行」收进展开详情；55 行原文与行号保留 | r9-maintenance（含展开详情） |
| 资产详情 · 运行监测 | 57 项计量带器件名，按机构分组，支持搜索和来源筛选；指标和单位中文；计算口径写成中文说明，未确认点位标「候选口径（待现场确认）」；保留 15 秒刷新、时间、质量、异常计数和读数历史；醒目标注「模拟数据」「真实硬件尚未接入」 | r9-maintenance |
| 资产详情 · 寿命与维护 | 预警优先展示器件、阶段、使用量、距阈值、剩余天数和「生成工单」；规则、状态、阶段、数据不足原因中文；测试规则依据标「模拟 / 测试阈值规则」 | r9-maintenance（含工单对话框） |
| 资产详情 · 历史 | 结构化映射为中文摘要；审计原文不改写 | r9-maintenance |
| 资产详情 · 接入准备 | 槽位和点位显示名称与中文类型；下拉框改为「整数（INT）」等；待确认值显示「10 吨」 | r9-maintenance |
| `#/locations` 位置 | 层级说明、类型、坐标系中文；错误中文 | 视口 + 错误态 |
| `#/catalog` 目录 | 去掉「EQUIPMENT CATALOG」；状态中文，「平台公共 · 只读」，「第 N 版」 | 视口 + 错误态 + catalog-override |
| `#/maintenance` 维护与到期中心 | 器件列显示器件名、机构和类型；类别、规则、审批、状态中文；SIM 与 REAL 筛选保持 | 视口 + r9-maintenance |
| `#/portfolio` 项目组合 | 去掉英文眉标；排期错误改用 `errorZh` / `validationZh`；日历标注「北京时间，第 N 版」 | 视口 + portfolio-* / r8-schedule 守卫 |
| `#/system/people(/:id)` 人员 | 操作历史的动作和结果中文，未知时显示「其他操作 / 结果未知」；账号服务未配置的说明中文 | p1-personnel 全部用例 + 手机 375/390 |

Workspace（演示）和 CloudStatus（仅开发环境）不在生产路由中，未作改动。

## 3. 反例测试（检测器不能全放行）

`r9-ux-localization.spec.ts` 中「检测器反例」用例的断言分两类：

- **必须拦截**：`DRAFT_UNAPPROVED`、`PCT_100`、`CONTACTOR`、`platform_admin`、单位 `count`、`edge-counted rising edges`、`ON duration`、`EQUIPMENT CATALOG`、「ASSET CENTER / 资产中心」、`Forbidden`、`Failed to fetch`、`name is required`、`stale`、`[object Object]` 等。
- **必须放行**：`LHK-CRANE-001`、`SIM-WO-…`、`DB505.DBX32.0`、施耐德 `LC1G205KUEC`、`QZ10t-31m`、`GB/T 3811-2008`、PLC / RFID / MQTT、「整数（INT）」、「旧类型（编码 CRANE，保持不变）」、`U·DAO`、`v1`。

另外，vitest `zh.test.ts` 覆盖了以下几点：错误文本不含 4 个以上连续的英文字母；结果未知和请求未发送的语义；51 种类型与后端一致；未知枚举显示为「未知」。

## 4. 本地验证（提交 85cb044 的工作树）

| 项 | 结果 |
|---|---|
| vue-tsc / eslint / vite build | 0 错误 / 0 问题 / 成功 |
| vitest | 34 个文件，251 passed，另有 1 个原有的预期失败 |
| Go：`internal/webauth`（新增 `TestLoginRedirectRequestsChineseUI`）、`internal/metering`、`services/cloud-api/...` | ok；gofmt 和 go vet 干净 |
| 契约测试（ci.yml 第 26 行全部 18 个文件） | 101 pass、0 fail；1 skip 为原有的 nginx 二进制环境门槛，CI 中实际执行 |
| Playwright premium（全部 27 个 spec，分批执行） | 全部通过：r9-ux-localization 11、r9-maintenance 7、p1-personnel 9、portfolio-* 21、r8-schedule + homepage-h2 19、r6 / location / asset-guards / equipment-remove 18、catalog / inventory-registration / identity-isolation / asset-guards 31、premium + asset-route-switch + command-scope 21、location-project-switch / r7 / r8-zero / r5-mobile 17、homepage-h1-acceptance 14、homepage-h1-r1 23、homepage-h0 + r5-geo-forensic 10、r5-geo-framing 14（拆为 5 次运行） |
| gitleaks（0d6e862..HEAD） | no leaks |
| 断言数量检查 | 本次改动的测试文件中，没有一个的 expect / assert 行数减少 |

无法在本地运行、以 CI 为准的有三项：demo / cloud 项目（需要 chrome channel）、b4（需要 Docker 和真实 Keycloak）、`tests/integration/run.mjs`。其中 `run.mjs` 第 89 行已从 `/Last Known State/` 改为 `/最近状态/`。

**测试改动说明：** 只因文案变化而更新期望值，断言强度不变。涉及的替换包括：「安装 Equipment」→「安装器件」、「（WORKSHOP）」→「（车间）」、取样时间提示去掉 `sampledAt` / `generatedAt` 字样（时间值与“服务端单一快照”仍逐字断言）、`Last Known State` →「最近状态」、REAL →「真实」。

## 5. 回执

```
CHINESE_UI_ROUTE_COVERAGE=PASS(local; 12 production routes × 5 viewports + asset tabs/dialogs + personnel; demo/cloud/b4 PENDING_CI)
RAW_ENUM_LEAKS=0(local guard)          ENGLISH_FORMULA_LEAKS=0(local guard)
BILINGUAL_DECORATION=0(local guard)    LOCALIZED_ERRORS=PASS(local; 403/404/500/503/network positive + vitest)
EQUIPMENT_51_TYPES=PASS(vitest vs types.go)   LHK_METERS_57=PASS(grouped by mechanism, names, search, source filter; mock)
HISTORY_LOCALIZATION=PASS(structured mapping, audit text unchanged)
PERSON_DELETE_UAT=PASS(local premium incl. mobile delete dialog)   MOBILE_375_390=PASS(local, no page overflow)
LOGIN_ZH_CN=PASS(local Go ui_locales) / PENDING_CI(b4 real Keycloak login + first password page)
SCHEMA=16   CI_9_OF_9=PENDING(OWNER push 85cb044 + docs commit)
EXISTING_DATA_PRESERVED=YES(no migration, no reset, no re-import, no realm re-init)
SIM_REAL_ISOLATION=UNCHANGED(display only; badges kept)   READY_FOR_INTERNAL_USE=PENDING(CI 9/9 + server deploy)
REAL_HARDWARE_CONNECTED=NO
```

## 6. 服务器部署（待 CI 9/9 后执行）

1. `git checkout --detach <同 SHA>`，然后执行 `node deploy/scripts/local-dev-deploy.mjs build|plan|apply`。plan 应显示 schema 16→16、无迁移。
2. 执行 `docker compose restart keycloak`（或通过手册中的 compose 包装执行），让主题文案生效。不导入 Realm，不改用户。
3. 验收：
   - 用英文浏览器打开登录页，应显示中文「安全登录」；首次改密页显示「设置新密码」。
   - 资产详情的四个标签页、维护中心、人员页在手机上无英文枚举。
   - 老河口 57 项模拟计量按机构分组显示。
4. 本轮完成后停止集中研发，等待 OWNER 提供真实行车接口。
