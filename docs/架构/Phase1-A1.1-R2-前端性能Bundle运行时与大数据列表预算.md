# Phase1-A1.1-R2 前端性能、Bundle、运行时与大数据列表预算

## 1. Baseline

REA 静态基线（由 CC 第四轮采集）：Three vendor 约 970KB raw，charts/ECharts 约 1127KB raw。该数字作为 before baseline，B10 必须重新用实际 build 复测，不把估算当最终结果。

## 2. Bundle 策略

- Vue routes lazy；
- Operations 默认不加载 Three；
- Twin Tab 才加载 Three/GLTF/postprocessing；
- ECharts 使用 `echarts/core` 只注册 Line/Bar/Gauge、Grid/Tooltip/Legend/DataZoom/CanvasRenderer 等实际能力；
- History/Portfolio schedule 才加载 charts；
- Topology 的 Vue Flow 只有 PoC 通过才纳入。

## 3. 预算（设计目标）

- initial app raw ≤220KB、gzip ≤70KB：估算目标，B10 根据真正代码重新冻结；
- 普通 route chunk 尽量 ≤120KB raw；
- charts 按需目标 <500KB raw；
- Three 可以仍较大，但必须 opt-in。

不要为了达数字而做危险 minification/移除错误处理。

## 4. DataGrid

Server-side paging/filter/sort；Virtual 只渲染当前已加载的大量行。固定 row height 优先，避免复杂 cell。列对象保持稳定，滚动不触发整个表体 rerender。

## 5. Server State

TanStack Query 候选：缓存/取消/targeted invalidation。避免同一 Asset 详情多个 tab 各自重复请求。设置合理 staleTime，不让 focus refetch 造成工业现场页面闪动。

## 6. Runtime Stream

WebSocket samples → lightweight frame buffer；Three rAF 读取 latest；UI 2~5Hz；charts 1~2Hz batch。中间显示帧可丢，但服务器历史不能丢。

## 7. Three

Budget：Premium 50~60FPS，Balanced≥40，Reduced≥30；DPR≤1.5；draw calls<350；triangles<700k；renderer memory route cycles 不增长。

避免：每帧 raycast、DOM measure、Vue watchers、new tween、全量 HTML labels。

## 8. History

每 series 默认 2k~5k points 目标；长范围自动 server downsample。30d 历史不能返回百万 raw points 给浏览器。

## 9. Network

列表 request 支持 AbortController/cancellation；搜索 debounce；Ctrl+K 结果限制；详情首屏先 critical fields，再 lazy tabs。

