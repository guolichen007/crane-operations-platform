# Web Console 冷加载与缓存测量（R5-UX-PERF）

只用于对比：同一构建产物、同一桩 API、同一网络限速，仅替换 nginx 配置。

1. 构建：`VITE_DATA_MODE=api VITE_AUTH_MODE=oidc npx vite build --outDir /tmp/w/dist-new`（在 `apps/web-console`）。
2. 启动 nginx（路径仅在测试副本中改写，见 `tests/contract/web-nginx.test.mjs` 的同一做法），上游指向 `python3 stub-cloud-api.py`（127.0.0.1:18081）。
3. 冷加载：`node tools/perf/web-load/coldload.mjs <label> http://127.0.0.1:18080 out.json`
   网络：150 ms RTT、1.6 Mbit/s 下行、750 kbit/s 上行；禁用缓存；按 HTML/JS/CSS/GeoJSON/字体/API 统计实际传输字节。
4. 二次访问：`node tools/perf/web-load/warm-cache.mjs <label> http://127.0.0.1:18080 out.json`（持久化浏览器配置）。

结果是 SwiftShader 无头浏览器的本机实测，不代表线上用户网络；线上数值须在服务器部署后同条件复测。
