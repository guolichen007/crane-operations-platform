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
