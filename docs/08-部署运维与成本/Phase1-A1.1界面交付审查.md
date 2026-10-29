# A1.1 Premium 界面交付审查

本轮按 2026-10-28 v2 任务书，在既有 A1 上升级 Web。品牌封面、认证门、全国沙盘 HUD、项目中心、设备列表/详情、告警中心及程序化行车孪生均已实现。未执行服务器部署、DNS/TLS 修改、main 合并、账号创建或真实设备接入。

## 本地查看

`npm run dev` 为原有离线演示沙盘。生产视觉需设置 `VITE_DATA_MODE=api`、`VITE_AUTH_MODE=oidc` 后启动 Vite；没有 BFF 时封面显示登录服务不可用，可重试。真实登录只跳转 `/auth/login`，Vue 不收集用户名和密码。Playwright 使用匿名 API/Session fixture 截取生产页面，不创建验收账号。

```powershell
$env:VITE_DATA_MODE='api'
$env:VITE_AUTH_MODE='oidc'
npm run dev -- --port 5175 --strictPort
```

已配置 1920×1080、1366×768 的登录、认证总览/只读标识、告警截图及 WebGL fallback、设备详情截图；CI frontend 归档匿名 PNG。截图与报告为忽略目录中的生成物，不进入 Git。当前是截图证据加布局/行为断言，尚未建立经负责人批准的跨提交像素 golden；不能把截图生成等同于像素回归通过。

## 单域名与主题

入口和 Keycloak 均为 `https://crane.youdaozd.com`。issuer 为 `/realms/crane`，BFF callback 为 `/auth/callback`。生产 Compose 保持原有五服务、固定 project 和基础镜像 digest；Keycloak `u-dao` 主题从仓库以只读 volume 挂载，realm 模板指定 loginTheme。继承官方 keycloak 主题，仅覆盖 CSS/messages，未 fork Freemarker。

共享 ingress 与 Crane 连接明确的 external frontend network，通过 Docker DNS 将 `/realms/`、`/resources/` 转发到 keycloak:8080，`/auth/`、API 与 `/` 转发到 web-console:8080，再进入 BFF。使用变量 proxy_pass 和 Docker resolver，宿主 loopback 仅作运维 fallback。Keycloak 路径保留其 nonce-aware CSP，避免应用 CSP 破坏官方表单脚本。`/admin/`、`/health`、`/metrics` 拒绝公共入口。旧 auth 子域名模板仅为历史回滚参考，A1.1 不启用第二入口。

所有模板均未激活。后续获准部署仍遵守 A1 的 clean source、磁盘、固定 project、HTTPS discovery 首次启动顺序。已有双域名环境切换 issuer 时需受控核验 `(issuer, sub)` 绑定及会话，不自动迁移身份。真实账号、密码、凭据和客户 Inventory 不属于本次交付。

## 数据与性能边界

模型 X/Y/Z/G 分组表达运动关系，轨道枕木/护栏使用 InstancedMesh。通用模型不是实际设备几何标定；超出可视范围时仅限制模型位置，原始 API 读数仍保留。抓斗协议当前无开度字段，显示未知；真实设备没有假动画。DEMO 模型动画有明确标识。

告警协议只有 code/text，页面不推断严重等级、不生成处理状态、不提供虚构确认。历史趋势未提供时显示暂无趋势，不绘制随机曲线。API/RBAC、项目范围、Last Known State / LIVE 租约机制保持原实现，观察员无法申请 LIVE。

HalfFloat composer、末端 ACES tone mapping、选择性 Bloom/三类 Outline、SMAA 和极轻 Vignette 已接入；renderer 为 NoToneMapping，避免重复映射。全国不启用 SSAO；本轮不启用可选 SSAO/LUT，避免无证据增加成本。画质依据初始能力与连续三段低 FPS 自动下降，DPR 上限 1.5/1.25/1，尊重 reduced motion。隐藏页停止更新，卸载释放 RAF/observer/listeners/几何/材质/纹理/实例 buffer 并主动丢弃 WebGL context。

已测孪生预算（整帧 62 draw calls / 1,861 triangles）、20 次行政区下钻及 20 次全国—项目—设备全过程，末段 GPU geometry/texture 没有增长。桌面独立 GPU、三个档位持续 FPS 及 Heap 分析仍需专门性能验收，不能用软件 WebGL 的截图或单个 FPS 读数代替。

## 负责人审查点

保留 Public 例外，不改变仓库可见性和根许可证。只允许推送本次单一功能分支，不移动基线 tag、不推 main、不批量推 tag。2026-10-28 的自动审批拒绝在 2026-10-29 负责人明确授权后解除，已推送功能分支并触发 CI。八项门禁、真实 Keycloak 同域演练和新 source SHA 镜像/digest 的最终状态以同 SHA 的 Actions 记录和发布清单为准，不得将本地测试或旧 SHA 的结果写成当前发布成功。Draft PR 创建受到连接器 403 限制，未申请额外权限。
