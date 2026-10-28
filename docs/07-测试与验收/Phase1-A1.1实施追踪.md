# A1.1 Premium 实施追踪

负责人于 2026-10-28 指定 v2 任务书，本轮在 `3525a351aa3677b15754541ff13e9f27e5e0685f` 上升级现有 Web。工作开始时 main 已在此提交；不回退 main，不移动 baseline/phase1-a1，不执行部署。

## UI0 架构审计

Vue 3 / Vite / TypeScript / Pinia / Vue Router、Three.js 0.186.1、postprocessing、camera-controls、GSAP 3.15、ECharts 6、Lucide 均已存在。App.vue 无认证挂载边界；SessionStatus 仅展示状态。main.ts 静态导入各路由，存在首屏负担。InventoryOverview 保持真实资产与运行来源分离，但缺少运营 HUD。CloudDevices 保留 Last Known State / LIVE 租约和权限边界。

MapSceneManager 已管理 GeoJSON 行政区下钻、全国/省/市/项目/设备镜头、选择、NetworkLayer、CSS2D 标签、资源释放。SpatialFloor 有抗锯齿网格与扫波，PremiumSceneBackground 有渐变 shader。PostProcessing 为全局 Bloom + Vignette；CraneMarkerFactory 是全国标记简模。结论：升级现有 direct Three.js，不更换框架或 UI 库。

旧界面截图保存在忽略目录 `tools/.cache/ui0-before.png`，不提交生成物。用户效果图仅作构图参考，不作为业务背景贴图。

## 技术参考映射

| 官方来源 | 采用的技术 | 本仓库落点 |
| --- | --- | --- |
| https://github.com/pmndrs/postprocessing | HalfFloat composer、SelectiveBloom、Outline、SMAA | PostProcessing.ts |
| https://github.com/yomotsu/camera-controls | smooth transition、视口偏移 | CameraRig.ts、CraneTwinScene.ts |
| https://threejs.org/examples/ | InstancedMesh、材质、资源生命周期 | CraneTwinFactory.ts |
| https://echarts.apache.org/handbook/en/concepts/style/ | 注册主题与语义色板 | charts/theme.ts、Chart.vue |
| https://www.keycloak.org/ui-customization/themes | 继承 bundled theme、CSS/messages | deploy/keycloak/themes/u-dao |

仅借鉴工程机制，自行实现组件和几何；未复制参考项目 UI/资产，未引入额外整站依赖或指定根许可证。

## 实施顺序与验收

UI1 登录门与 Keycloak 同品牌/同域模板；UI2 tokens 和组件；UI3 AppShell 与运营 HUD；UI4 后处理和自适应画质；UI5 四轴数字孪生；UI6 项目设备告警；UI7 行为/视觉/生命周期回归；UI8 全检查与交付。每阶段独立中文 Conventional Commit。

测试先执行 contract、lint、test、build，再执行 Compose 集成。性能数值必须注明浏览器与 GPU 环境；软件 WebGL 截图不能代替真实桌面 GPU 的 FPS 验收。截图/报告/发布生成物均放忽略目录，CI 保留 8 个原有 job。

## 2026-10-28 本地实际结果

UI0–UI7 已分阶段提交；UI7 复核追加修正 Vignette 二次乘色、整体渲染统计与上海日期筛选。UI8 已补充单域名交付手册、原手册修订和审查点，发布门禁未冒充完成。

| 验证 | 实际结果 |
| --- | --- |
| `node tools/task.mjs contract` | PASS，REST/MQTT 生成契约无漂移 |
| `node tools/task.mjs lint` | PASS，包含 Go vet/fmt/staticcheck、vue-tsc、ESLint；本机 staticcheck 使用已有模块缓存的 file GOPROXY，未改版本或跳过检查 |
| `node tools/task.mjs test` | PASS，Go 全部包、Vitest 54 项、Node 契约/部署门禁 16 项 |
| `node tools/task.mjs build` | PASS，Go + Vite 生产构建；生成物忽略 |
| Playwright | 原有及 Premium 整套 44 项通过；新增完整 20 轮生命周期 1 项和上海日期筛选 1 项分别通过，共 46 项；最后调色后 Premium 8 项、强化预算/20 轮测试再次通过 |
| `node tools/task.mjs security` | PASS，仓库预检、npm audit 0 漏洞、govulncheck 无漏洞；不等同于 CI Trivy/gitleaks 完成 |
| `node tools/task.mjs production-contract` | PASS，五服务、固定 project、只读 Keycloak 主题及一致 issuer |
| Compose / A1 身份集成 | 未通过：初始本机 Docker 未运行；启动后引擎 API 未响应，终止等待。真实 Keycloak E2E、普通 Compose 集成和备份恢复不得记为通过 |
| 远程 8 项 CI / release | 未执行：推送被自动审批拒绝，等待对明确远程仓库/分支的直接授权 |

环境为 Windows、Node 24.19.0、Go 缓存工具链、headless Chrome，允许软件 WebGL。Premium 1920×1080 / 1366×768 截图经本地查看，结果在 `tools/.cache/delivery-qa`；旧视觉、完整回归、强化预算分别位于 `tools/.cache/ui0-before.png`、`tools/.cache/final-ui-qa`、`tools/.cache/performance-qa`，不提交生成物。

孪生场景 premium 档整帧统计（关闭 renderer 自动逐 pass 清零）为 **62 draw calls / 1,861 triangles / 39 geometries / 38 textures**，测试同时断言非空场景下限，避免只统计最终 fullscreen pass。20 次行政区下钻往返每次 148 geometries；20 次全国→Project-A→单机→全国，每次 194 geometries / 11 textures，末段没有增长。DEMO 地图旧性能回归读到约 60 FPS / 249 draw calls / 323,229 triangles，但其部分测试缩小 deviceScaleFactor，此读数不作为实际 1080p 桌面帧率验收。

未完成的正式验收：负责人确认像素 golden、目标硬件持续 FPS/Heap、真实 Keycloak 同域主题与 OIDC 浏览器演练、8 项 CI 和新 source SHA 镜像/registry digest。未部署、未合并、未移动基线 tag。
