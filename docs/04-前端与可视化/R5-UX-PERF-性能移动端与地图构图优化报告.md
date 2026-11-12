# R5-UX-PERF 性能、移动端与地图构图优化报告

- 分支：`feature/crane-r5-ux-perf`（自 RC2 冻结源码 `973e134de99cbede898b6e3098a345312ddddd6c`）
- 范围：A Crane Web Nginx 压缩与缓存分层；B 手机端横向溢出；C 全国/省/市地图构图与全国侧墙；D 运行入口措辞与空资产编号。
- 不在范围：main、RC2 标签与线上 RC2、共享 u-dao Ingress、数据库与迁移、Keycloak、MQTT、Firebot、Baobei、真实设备、部署。
- 证据目录：`docs/04-前端与可视化/evidence/r5-ux-perf/`（`http/`、`map/`、`mobile/`）。
- 本地环境：SwiftShader 无头 Chromium 156、nginx 1.18（Ubuntu 包，仅用于运行同一份配置）。数值均为本机实测，不是线上用户网络的承诺。

## 1. 结论

| 项目 | 修复前（973e134） | 修复后 | 状态 |
|---|---|---|---|
| 静态资源压缩 | 无 `Content-Encoding`，无 `Vary` | JS/CSS/JSON/GeoJSON 均 gzip，`Vary: Accept-Encoding` | 本地 PASS，CI 待核 |
| 缓存分层 | 全部无 `Cache-Control` | 仅内容哈希的 `/assets/*-xxxxxxxx.*` 为一年 immutable；其余 `no-cache` 重新验证；API/认证不加头 | 本地 PASS |
| 首页冷加载（3D，限速同条件） | 17.0 s，3 122 KiB | 5.7 s，824 KiB | 实测 |
| 首页冷加载（2D，限速同条件） | 9.3 s，1 514 KiB | 3.0 s，423 KiB | 实测 |
| 375 px 横向溢出 | 首页 462、资产台账 664、资产详情 443、位置中心 556 | 11 个页面全部等于视口宽度 | 本地 PASS |
| 全国侧墙在下钻后可见 | 省/市/县均可见 | 仅全国可见；返回全国恢复，始终只有 1 个 | 本地 PASS |
| 全国地图宽度填充（1920×1080） | 0.62，左右 214/188 px | 0.87，左右各 69 px | 本地 PASS |
| 「最近状态 / LIVE」 | 有运行身份即显示 | 「查看运行状态」 | 本地 PASS |
| 空资产编号 | 显示「—」 | 「编号待补全 · 内部 ID …」 | 本地 PASS |

关于 CC 报告的几点，本轮按源码和实测核对后的处理如下。静态资源未压缩属实，按 P1 性能问题处理，只改 Crane 自己的 Web Nginx，不改共享 Ingress。Three.js 懒加载已经存在：实测 2D 首页不请求 `three-*.js`，3D 首页才请求；`charts-*.js`（1.1 MB）两种首页都不加载。因此本轮不重复开发懒加载。「最近状态 / LIVE」是入口措辞问题，链接条件只是“有运行身份”，并不代表在线；真正的 LIVE 仍只由运行详情页按运行状态显示。

## 2. A：Crane Web Nginx（`deploy/nginx/production.conf`）

改动：

- `gzip on; gzip_vary on; gzip_proxied any; gzip_comp_level 5; gzip_min_length 1024`。只压缩 HTML、CSS、JS、JSON、GeoJSON、SVG、manifest；PNG/JPEG/WebP/woff2 等已压缩格式不在列表内。
- `/api/`、`/auth/` 位置块里 `gzip off`。这些响应带会话和 CSRF 信息，不在本层压缩（避免 BREACH 类问题），代理、日志、超时、`proxy_buffering off` 全部不变。
- `map $uri $crane_cache_control` 决定缓存层级：
  - `^/assets/[^/]+-[A-Za-z0-9_-]{8}\.(js|css|woff2?|svg|png|jpe?g|webp)$` → `public, max-age=31536000, immutable`（Vite 内容哈希产物）。
  - `^/(api|auth)/` → 不加任何 Cache-Control，cloud-api 自身的 `no-store` 原样透传。
  - 其余（`index.html`、SPA 路由、`/geo/*.json` 未版本化地图数据、未哈希文件）→ `no-cache`，靠 ETag/Last-Modified 重新验证。
- `add_header Cache-Control … always` 放在 server 级，与原有安全头同级。没有任何 location 级 `add_header`，所以 CSP、nosniff、X-Frame-Options、Referrer-Policy、Permissions-Policy 不会被覆盖丢失。HSTS 和 Cookie 属于 Ingress 与 cloud-api，本层未改动。
- `location /assets/ { try_files $uri =404; }`：缺失的构建分块返回 404，不再把 index.html 当作 JS 返回。

原始 HTTP 证据：`evidence/r5-ux-perf/http/before-headers.txt`、`after-headers.txt`。同一个构建产物，单个资源的字节数：

| 资源 | 未压缩 | 修复前传输 | 修复后传输（gzip） |
|---|---:|---:|---:|
| `index-*.js` | 236 734 | 236 734 | 90 208 |
| `index-*.css` | 89 781 | 89 781 | 19 516 |
| `three-*.js` | 970 296 | 970 296 | 281 559 |
| `geo/geo-manifest.json` | 567 088 | 567 088 | 87 969 |
| `geo/china/100000.json` | 530 717 | 530 717 | 175 285 |
| `geo/provinces/330000.json` | 110 117 | 110 117 | 35 865 |
| `/api/v1/projects`、`/auth/session` | — | 不压缩 | 不压缩，`Cache-Control: no-store` 原样 |

`index.html` 带 `If-None-Match` 再次请求返回 `304 Not Modified`。

### 冷加载与二次访问（同一构建、同一桩 API、只换 nginx 配置）

网络限速：150 ms RTT、1.6 Mbit/s 下行、750 kbit/s 上行，禁用缓存，1920×1080。工具：`tools/perf/web-load/coldload.mjs`。每种情况跑 2 次，两次数值一致。

| 首页 | 修复前 就绪 / 传输 | 修复后 就绪 / 传输 |
|---|---|---|
| 3D（34 个省标签出现） | 17 034 ms / 3 197 358 B | 5 748 ms / 843 697 B |
| 2D（SVG 地图出现） | 9 343 ms / 1 550 356 B | 3 001 ms / 433 483 B |

按类别拆分（3D 首页，请求数 / 字节 / gzip 响应数）：

| 类别 | 修复前 | 修复后 |
|---|---|---|
| HTML | 1 / 1 581 / 0 | 1 / 1 606 / 0（928 B，低于 1 KB 阈值不压缩） |
| JS | 18 / 1 422 549 / 0 | 18 / 463 300 / 11 |
| CSS | 2 / 105 577 / 0 | 2 / 24 507 / 2 |
| GeoJSON | 3 / 1 666 888 / 0 | 3 / 353 521 / 3 |
| 字体 | 0 | 0（项目不加载网络字体） |
| API（桩） | 8 / 763 / 0 | 8 / 763 / 0 |

二次访问（`tools/perf/web-load/warm-cache.mjs`，持久化浏览器配置，nginx 访问日志为证）：修复后，哈希资源全部直接命中浏览器缓存，没有一个请求到达 nginx；`index.html`、`geo-manifest.json`、`china/100000.json` 以 `304` 重新验证，0 字节正文。修复前因为没有 Cache-Control，Chromium 只能做启发式缓存，结果取决于 Last-Modified，不可控。证据：`http/warm-*-nginx-access.log`。

这些数值来自本机无头浏览器的限速模拟。CC 报告的线上 10.8 s / 27.8 MB 需要在部署后用相同网络条件复测，不能把本报告的数值当作线上结果。

### 测试

`tests/contract/web-nginx.test.mjs`（已加入 CI `repository-contract` 作业）：

- 静态 4 项：gzip 与类型、缓存分层规则、安全头与 API 代理不变、共享 Ingress 示例未加压缩或缓存。
- 真实 nginx 1 项：用同一份配置启动 nginx，上游是 Node 桩，逐项断言 gzip、Vary、immutable、no-cache、304、缺失分块 404、API `no-store` 与 Set-Cookie 属性透传、安全头齐全。CI 环境（GitHub ubuntu runner 自带 nginx）下不允许 SKIP，找不到 nginx 时直接失败。

反证：把配置换回 973e134 版本后，5 项中 4 项失败（只剩“共享 Ingress 未改”一项通过）；换回修复版后 5/5 通过。

## 3. B：手机端横向溢出

`tests/e2e/premium/r5-mobile-overflow.spec.ts` 覆盖 11 个页面：首页 3D、首页 2D、项目组合、项目、行车资产、资产台账、资产详情、位置中心、设备目录、运营、告警。宽度为 375、390、768、1366、1920。断言内容：

- `document.scrollWidth` 不大于视口宽度。
- 列出右边越界的可见元素；放在可横向滚动容器内的不算。
- html、body、#app 不得用 `overflow-x: hidden/clip` 掩盖溢出。
- 侧栏不得盖住内容；手机端侧栏只显示图标。
- 地图上的控件之间不得互相重叠。

修复前 375 px 实测（`mobile/metrics-375-before.json`）及逐项修复：

| 页面 | scrollWidth | 越界元素 | 修复 |
|---|---:|---|---|
| 首页 3D / 2D | 462 | 页头的统计范围、时间窗、2D/3D 切换不换行 | `.home-head` 在 ≤700 px 时换行，统计范围允许折行 |
| 资产台账 | 664 | 两个筛选下拉框、筛选与刷新按钮 | `.asset-filters` 换行，搜索框独占一行 |
| 资产详情 | 443 | 编辑、移动、退役操作区 | `.detail-head`、`.detail-actions` 换行，详情网格改为单列 |
| 位置中心 | 556 | 类型、状态下拉框 | 与资产台账共用 `.asset-filters` 规则 |

另外修复了同时发现的两个视觉问题（宽度测量本身发现不了，已加入断言）：

- 375 px 时侧栏的“资产与设备”分组按钮和文字画到了内容上。现在 ≤700 px 一律使用图标式侧栏；桌面端保存的折叠偏好不受影响。
- 375 px 首页地图上，「全国态势/设备聚焦」与「运营视角/立体视角」两组按钮重叠，「行政区/资产/告警」与右下工具重叠。现在分别错开位置。

宽表格改为在自己的 `.data-table-wrap` 内横向滚动，不再被裁掉。修复后 5 种宽度、11 个页面全部等于视口宽度（`mobile/metrics-*.json`）。截图：`mobile/before-375-*.jpg`、`after-375-*.jpg`、`after-390-*`、`after-768/1366/1920-overview.jpg`。

## 4. C：全国、省、市地图构图

### A. 全国侧墙的生命周期

973e134 中，`mergeSideWalls()` 把 34 个省的侧墙合并成一个 Mesh（`merged-side-flow`），直接加入 scene，不属于 `country`。之后没有任何代码再引用它。所以省级把 `country` 设为半透明后，侧墙仍然完全不透明；市、县和项目聚焦把 `country` 隐藏后，侧墙依然存在（见 `map/compare-1920-110000.jpg` 左图）。

修复：在 `MapSceneManager` 中用 `countryWalls` 保存这个 Mesh 的引用，由 `syncCountryWalls()` 统一决定是否显示，规则是 `country 可见 && 当前层级 = 全国 && 没有聚焦项目`。在 `init`、`enter`、`back(0)`、`focusProject` 中都会调用。没有把它挂进 `country.children`，因为多处代码默认那里只有区域分组：`active`、`visibleGroups`、`dimCountry`、`geometrySamples`、拾取、标签。资源重试时 `init()` 会先释放旧的侧墙、省份和外轮廓，不会重复添加。

浏览器验证（three.js 自带的 `__THREE_DEVTOOLS__` 钩子读取真实场景图，不需要修改产品代码）：

- 全国：侧墙 1 个，可见。
- 北京、浙江、广东、海南、嘉兴、海宁：侧墙 1 个，不渲染。
- 返回全国：侧墙 1 个，可见。
- 资源加载失败后重试：侧墙 1 个，34 个省份。
- 连续 4 次「下钻到海宁 → 返回全国」：geometries 第一轮从 82 升到 85（基线也是这样，是首次下钻时缓存的共享几何），之后保持不变；textures 不变；全国 draw calls 保持 120，与基线相同。

### B. 动态取景

继续使用原有的 `CameraRig.frame()`，二分求距离的算法不变，只是取景目标换了：

- 之前：固定按画布的 62%（全国）、52%（省）、46%（市）取景。
- 之后：`MapSceneManager.safeInsets()` 实时读取舞台上各个控件（面包屑、视图切换、观察视角、场景模式、地图工具、南海附图、版权说明）的实际位置，按位于上半部还是下半部，分别转换成顶部和底部的避让带；左右留出与画布宽度成比例的边距（最少 24 px）。再减去右侧抽屉遮住的宽度，得到安全取景框。`frame()` 让区域在宽或高中先达到边界的那个方向上充满安全框，并在水平和垂直方向都居中。
- 这样不需要为不同屏幕尺寸设置常数，也没有把相机距离乘以任何固定系数。拿不到 DOM 时（例如单元测试）仍退回原来的预设比例。
- 全国「设备聚焦」保留原有约 1.13 倍放大和向设备加权中心的偏移，偏移量受画布边界限制，不会出界。
- 省、市取景只看主体陆地：先从面积最大的多边形开始，把距离不超过主体尺寸 25%、面积至少占 2% 的多边形逐步并入，然后一次性加入与之相邻的小岛。所以舟山、南澳会被纳入，海南的三沙、广东的东沙不会把主体挤到角落。三沙、东沙仍然绘制，也可以点击。
- 县级不再把整个框固定放大 40%，改为按实际边界加上项目标记和标签位置取景。
- 新增「适配当前区域」按钮（地图右下角）。它只重新取景，不改变层级，也不改动 URL。

各分辨率实测：

| 视口 | 层级 | 修复前 宽/高填充率 | 修复前 左右边距 | 修复后 宽/高填充率 | 修复后 左右边距 |
|---|---|---|---|---|---|
| 1366×768 | 全国 | 0.62 / 0.63 | 209 / 188 | 0.66 / 0.69（高度先到边界） | 176 / 176 |
| 1366×768 | 北京 | 0.52 / 0.66 | 226 / 276 | 0.45 / 0.69（高度先到边界） | 290 / 290 |
| 1653×1186 | 全国 | 0.62 / 0.27 | 173 / 150 | 0.87 / 0.39 | 55 / 55 |
| 1653×1186 | 北京 | 0.52 / 0.31 | 194 / 215 | 0.87 / 0.62 | 55 / 55 |
| 1920×1080 | 全国 | 0.62 / 0.39 | 214 / 188 | 0.87 / 0.57 | 69 / 69 |
| 1920×1080 | 北京 | 0.52 / 0.43 | 237 / 271 | 0.80 / 0.79 | 105 / 105 |
| 1920×1080 | 浙江 | 0.52 / 0.46 | 260 / 248 | 0.75 / 0.79 | 130 / 130 |
| 1920×1080 | 广东 | 0.52 / 0.34 | 238 / 269 | 0.87 / 0.64 | 69 / 69 |
| 1920×1080 | 海南 | 0.52 / 0.80（含三沙，主岛缩在角落） | 251 / 257 | 0.87 / 0.71（主岛） | 69 / 69 |
| 1920×1080 | 嘉兴 | — | — | 0.87 / 0.74 | 69 / 69 |
| 1920×1080 | 海宁 | — | — | 0.87 / 0.65 | 69 / 69 |
| 2560×1440 | 全国 | 0.62 / 0.56 | 341 / 304 | 0.86 / 0.81 | 116 / 116 |
| 2560×1440 | 北京 | 0.52 / 0.60 | 371 / 444 | 0.59 / 0.81（高度先到边界） | 349 / 349 |

填充率的分母是“画布减去抽屉遮挡后的宽高”；边距指区域投影边界到画布边的像素。全部数据见 `map/framing-metrics.json`。

1366×768 的全国画布只有 1044×540，按宽度算的填充率提升不多。原因是高度先到达安全框边界：上方有切换按钮，下方有工具栏。1366×768 的北京同理，北京的形状偏高。这是在完整显示区域前提下的上限，没有为了提高数字去裁掉地图。

### C. 省级表现

- 运营视角的俯角：省级从 47° 提高到 57°，市级从 50° 提高到 61°。偏航（yaw）也调小，地市和区县的边界更正、更清楚。立体视角保持原值，所以两种视角有明显区别（测试 5 验证：运营视角俯角 ≥55°，且大于立体视角）。
- 省、市、县的顶面高度保持原值（1.32 / 1.17 / 1.08），不影响标记、标签和拾取的高度。侧墙高度从 0.42 / 0.27 / 0.18 降到 0.24 / 0.16 / 0.11，并且不超过该区域实际尺寸的 4%。所以小县不会再出现与省同样高的“墙”（见海宁截图）。
- 材质、动画和后期处理都没有替换。

同屏对比：`map/before-provinces-beijing-zhejiang-guangdong-hainan.jpg` 与 `map/after-provinces-beijing-zhejiang-guangdong-hainan.jpg`；逐个对比见 `map/compare-1920-{country,110000,330000,440000,460000}.jpg`。

### D. 标签与“未定位”提示

- 全国视图里，设备数为 0 的省份只显示省名（如「北京」），文字略淡；有设备的省份仍显示「浙江 · 2台」。鼠标悬停时显示「新疆 · 0台」。`title` 中是完整名称和数量，`data-device-count` 仍是原始数值。统计接口和计数规则没有改动。
- 所有项目都没有行政区时，地图上方显示「已授权项目均尚未定位（未设置行政区），地图不显示其资产；统计数字不受影响。」地图上没有任何项目或资产标记，也不会生成坐标。KPI 依然来自服务端（测试 10）。

## 5. D：运行入口措辞与资产编号

- `InventoryDevices.vue`：链接文字从「最近状态 / LIVE」改为「查看运行状态」（`RUNTIME_LINK_LABEL`）。显示条件仍是该资产有运行身份；没有运行身份的资产不显示入口。是否 LIVE 只在运行详情页按运行状态判断，这部分没有改动。
- 资产台账和资产详情：编号为空时显示「编号待补全」，并以小字注明「内部 ID …」（详情页还会附上「项目 …」）。不会自动生成业务编号。同名资产可以通过内部 ID 和所属项目区分。
- 单元测试 `tests/asset-display.test.ts` 有 3 项；浏览器测试在 `r5-mobile-overflow.spec.ts` 的 D 项。

## 6. 反证（新测试在 973e134 源码上的结果）

做法：用 `git stash` 移走 `apps/web-console/src` 的全部改动，其余（新测试本身）保持不变，然后运行新测试。

| 测试 | 973e134 | 修复后 |
|---|---|---|
| `r5-geo-framing` 1（4 种分辨率全国） | 4 项失败（填充率或居中不达标） | 4 项通过 |
| 2/6 北京侧墙 | 失败（侧墙可见） | 通过 |
| 3 浙江→嘉兴→海宁 | 失败 | 通过 |
| 4 广东、海南 | 2 项失败 | 2 项通过 |
| 5 运营/立体视角 | 失败 | 通过 |
| 7 Inspector / Resize / 适配当前区域 | 失败（没有该按钮，且填充不足） | 通过 |
| 8 Esc 与 URL 历史中的侧墙状态 | 失败 | 通过 |
| D 「0台」标签 | 失败 | 通过 |
| 10 全部未定位提示 | 失败 | 通过 |
| 7b、8b、9、9b | 通过（防回归用） | 通过 |
| 移动端 375 px | 失败（scrollWidth 462/664/443/556） | 通过 |
| 移动端 1920 px | 通过（防回归用） | 通过 |
| D 运行入口与编号 | 失败 | 通过 |
| vitest `camera-framing`（6 项）、`asset-display`（3 项） | 8 项失败 | 9 项通过 |
| `web-nginx` 契约（5 项） | 4 项失败 | 5 项通过 |

## 7. 本地回归（修复后工作区）

| 项目 | 结果 |
|---|---|
| vue-tsc、eslint | 退出码 0 |
| vitest | 29 个文件，227 项通过，另有 1 个原有 expected fail |
| 契约测试 | 原有 63/63，新增 web-nginx 5/5（真实 nginx） |
| premium 新增 | r5-geo-framing 17/17，r5-mobile-overflow 6/6 |
| premium 原有 | homepage-h1-acceptance 14/14；homepage-h1-r1 23/23；h2-1d 与 h0 共 26/26；asset、catalog、command、equipment、location 共 31/31；identity、inventory、portfolio、premium 共 51 通过，1 项为原有的真实 GPU 用例 skipped |
| demo | overview 14/14（含巡演 2 项），home-v033、spatial、industrial-b4 共 13/13，alarm-demo、b4-visual、premium-lifecycle 共 6/6 |
| cloud | 14/14 |
| 403/409/422/503 | 原有测试全部通过：identity-isolation、portfolio-grant-revoke、asset-route-switch、location-closure、h1-r1 的 503 与重试等 |
| 本地未运行 | b4 真实 Keycloak + PG（需要 Docker）、integration、Trivy、gitleaks、`node tools/production-contract.mjs`（需要 docker compose，在 973e134 上同样无法运行）。这些只看 CI 结果。 |

为配合新的取景方式，修改了一个原有测试：`tests/e2e/web-console/overview.spec.ts` 中两条“全国宽度 ≤ 0.68”“省级宽度 ≤ 0.58”的上限。这两条限制的正是被报告为“地图过小”的旧行为。现在改为“全国 > 0.68，且左右边距各 ≥ 4%”，省级为“≥ 0.45，且左右边距各 ≥ 4%”，并保留“区域完全在舞台内”的原有断言。改动后断言并没有更宽松：新增了边距和居中要求。

## 8. 剩余问题

| 级别 | 问题 | 说明 |
|---|---|---|
| P1 | 线上性能未复测 | 本报告的冷加载数值来自本机限速模拟，需要部署 R5 候选后在相同网络条件下复测。CC 报告的 27.8 MB 指的是整个 dist（含 21 MB 的 prefectures GeoJSON），首页实际只加载 3 份 GeoJSON。 |
| P1 | 业务写入闭环未在线上验证 | 本轮没有涉及。READ_ONLY_UAT 为 CC 报告通过；BUSINESS_WRITE_UAT 为 NOT_RUN。 |
| P2 | 未用 Brotli 或预压缩 | 本轮按要求不引入新模块。gzip 为运行时压缩，comp_level 5；若 CPU 不足，可改为构建时生成 .gz 并开启 `gzip_static`。 |
| P2 | GeoJSON 未带版本号，只能 no-cache 重新验证 | 二次访问仍会发起 304 请求。后续可以给地图数据加内容哈希，再纳入 immutable。 |
| P2 | `charts-*.js` 1.1 MB | 首页不加载，只有用到图表的页面才加载。暂不处理。 |
| P2 | 1366×768 全国填充率 0.66 | 受画布高度限制。要进一步放大需要重新安排首页 HUD 布局，不属于本轮。 |
| P2 | 三沙、东沙不在省级取景范围内 | 仍绘制且可点击，只是首屏不刻意框入。如果业务要求首屏完整显示海南全部海域，需要另行确定。 |
| P2 | 设备目录只能查询 | 制造商和型号的维护入口按要求不在本轮，转入产品待办。 |
| P2 | 资产台账浅色/深色主题、空字段折叠 | 不在本轮。 |

没有发现新的 P0，也没有发现数据或权限方面的回归。

## 9. 提交与 CI

由 OWNER 推送后补记。
