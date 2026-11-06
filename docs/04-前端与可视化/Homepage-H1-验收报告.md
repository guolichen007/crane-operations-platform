# Homepage H1 验收报告（H1-4）

> 分支 `feature/homepage-operations-center-r1`；H1 提交：H1-0 `b5d7a53`、H1-1 `c11bfd5`、H1-2 `c2beb80`、H1-4（本报告所在提交）。未修改 main，未部署，未启动 G3-D4，未新增后端接口，未制造工单/检验到期数据。
> 环境说明：以下全部为**本地** headless Chromium（chrome-headless-shell 1248）+ **SwiftShader 软件渲染** + mock API 的结果，不是 GPU 实机，也不是真实 Keycloak/PostgreSQL 环境。同 SHA 的 9 个 CI Job 结果需在 OWNER 推送后以 GitHub Actions 为准，本报告不预先声称。

## 1. 地图净可见面积：前后对比（同一口径、同一夹具）

口径见 H1-0 设计 §2：`|stage∩viewport| − Σ|overlay∩stage∩viewport|`，除以视口面积。前值用 `evidence/homepage-h1/measure-h0-before.mjs` 在 H0 提交 `76e1cef`（首页未改）上重新测量；后值由 `tests/e2e/premium/homepage-h1-acceptance.spec.ts` 测量并在 CI 的 `premium-ui-visual-<sha>` 产物中输出 `metrics-*.json`。

| 视口 | H0 前（76e1cef） | H1 三维 | H1 二维 | 门槛 | KPI 卡片 | 最小字号 | 横向溢出 |
|---|---|---|---|---|---|---|---|
| 1280×720 | 13.1 % | 40.2 % | 42.4 % | ≥ 40 % | 8 → 5 | 7 → 10 px | 0 |
| 1366×768 | 16.5 % | 42.2 % | 44.1 % | ≥ 40 % | 8 → 5 | 7 → 10 px | 0 |
| 1440×900 | 19.6 % | 48.1 % | 49.7 % | ≥ 35 % | 8 → 5 | 7 → 10 px | 0 |
| 1920×1080 | 27.6 % | 37.6 % | 38.6 % | ≥ 35 % | 8 → 5 | 7 → 10 px | 0 |
| 2560×1440 | 42.0 % | 44.3 % | 44.9 % | ≥ 35 % | 8 → 5 | 7 → 10 px | 0 |

H0 的 KPI 数与最小字号取自 `evidence/homepage-h0/layout-metrics-d97c0a7.json`。1280×720 的 40.2 % 仅高出门槛 0.2 个百分点，余量很小，后续任何在 stage 内新增浮层都会让该用例转红。

截图：`evidence/homepage-h1/after/home-h1-<w>x<h>-{3d,2d}.webp`、`home-h1-webgl-fallback.webp`；前后对照拼图 `evidence/homepage-h1/before-after-3d.webp`（左 H0、右 H1）。

## 2. 验收中发现并修复的问题（H1-4）

1. **1440 宽度地图不足**：原设计窄断点上限 1439，1440×900 三栏常驻实测 26.4 %。窄断点上限改为 1599，设计文档 §3 已同步修订。
2. **抽屉遮挡地图工具**：窄断点下选中项目时，固定定位的详情抽屉盖住了「重置全国视角」等右侧工具（由 demo 回归「保留行政区路径」在 1440 下暴露）。修复：抽屉打开时 stage 右侧控件内移（`--drawer-inset`）。新增 1280/1440 elementFromPoint 用例；反证：去掉内移后该用例失败（`重置全国视角 未被抽屉遮挡`）。
3. **二维视图不响应项目/资产选择**：`focusRequest` 仅由三维舞台消费，二维下 `?project=` 深链与 Inspector 点击均无效。修复：二维舞台消费同一请求并只做选择（无相机）。新增用例覆盖二维深链、点击设备、后退、切回三维保留选择；反证：屏蔽二维消费后该用例失败。
4. **Esc 关闭抽屉**（设计 §3 承诺）：三维在已选中但相机尚未进入 PROJECT/DEVICE 预设时，Esc 会先上退行政区；二维无 Esc 处理。修复：两视图均为「先关闭选择，再上退一级」。用例覆盖三维、二维。

## 3. 测试结果（本地，最后一次完整运行）

| 套件 | 结果 |
|---|---|
| premium `homepage-h1-acceptance.spec.ts` | 14 passed |
| premium `homepage-h0-defects.spec.ts` | 9 passed + 1 expected fail（H0-D2，`test.fail` 保留） |
| premium `premium.spec.ts` / `command-scope` / `asset-guards` | 21 passed，1 skipped（既有「真实 GPU 验收」用例，仅在真实 GPU 运行） |
| demo（7 个 spec） | 36 passed |
| cloud（3 个 spec） | 14 passed |
| vitest | 170 passed + 1 expected fail（H0-D1，`it.fails` 保留） |
| typecheck / lint / contract / build | 通过；构建中 `MapSceneManager`、`SpatialStage3D` 为独立 chunk，Overview 仅动态 `import()` |

本地运行受 bash 单次约 180 s 上限限制，premium/demo 按 spec 分批执行，未改动任何超时或断言。

### H1-4 验收项映射

- 五分辨率前后截图与面积：§1。
- 2D / 3D / WebGL 回退：五分辨率二维截图；`?view=2d` 不请求 `MapSceneManager`/`SpatialStage3D`（OIDC 模式）且 demo 模式不请求 `three`，切三维后按需加载；WebGL `getContext` 置空后自动二维 + 提示 + 三维按钮禁用，KPI 仍可操作。
- 深链 / 刷新 / 后退前进 / 项目与资产切换：D3 用例（真实鼠标逐级钻取、刷新恢复全路径、后退前进、`?region=330481` 直开、未知区域 replace 无循环、快速切换只停在最后目标）；项目/资产切换与后退；未知资产提示并清理；二维选择。
- KPI 动作与 API 来源：数值来自 `/api/v1/projects`、`/assets/devices`、`/devices`、`/alarms`；告警 KPI 点击过滤并写 `?status=ALARM`，Inspector 只列该设备；SIM 不计入「真实运行」；工单/检验显示「未接入」，不出数字。
- 空数据 / 错误 / 弱网 / 缩放：空项目明确空态、定位按钮禁用、无 NaN；`/projects` 503 显示可恢复错误，重试成功；每个请求延迟 2.5 s 时 KPI 显示「—」与「正在读取」而非 0；125 %（1024×576）无横向溢出且可钻取；D4 1280/1366/1920 真实鼠标 + 键盘 Enter + elementFromPoint。

## 4. 未完成 / 未验证（如实列出）

- **FPS、GPU 帧时间、首屏加载时间：NOT_RUN。** 本地仅 SwiftShader，不能代表 GPU；本报告不对性能作任何达标声明。
- **真实 Keycloak + PostgreSQL（B4 套件）：本地 NOT_RUN**（需 Docker），以 CI `b4-*` Job 为准；本批未改动认证、PATCH、409、RBAC、资产中心代码。
- **同 SHA 9 个 CI Job：待 OWNER 推送后确认**（本机 SSH 推送因 host key 校验失败，按要求不绕过）。
- **H0-D1（省份空值计入省份数）、H0-D2（告警时间按 UTC 显示）**：属 H2 统计数据修复项，反证保留为 expected fail，未删除、未掩盖。
- **默认视图**：实现默认三维（`?view=2d` 切二维），是否改为默认二维留待评审决定。
- **SideRail 折叠**：设计中 1280–1599 SideRail 折叠为 56 px 未实施（属全局壳层，超出首页范围）；当前面积已在不折叠的情况下达标。
- 登录封面（BrandLoginCover）在 OIDC 会话检查期间加载 `three`，为 H1 之前既有行为，未在本批修改。
- 地图底图仍为 DataV.GeoAtlas 开发边界，页面已标注「未经审图，公开展示前须替换」。
