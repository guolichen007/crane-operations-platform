# 本地 Web 远程兼容规范

API 使用相对 URL，WebSocket 从当前 origin 推导 ws/wss。不得写死现场 IP、端口、frps 地址或设备证书。
支持反向代理 host/prefix，正确处理静态资源 base、路由刷新、redirect 与 X-Forwarded-Host/Proto/For；只信任受控代理，防止任意用户伪造。
支持 HTTPS/WSS、正常 WebSocket Upgrade。Cookie 设置合理 Path、SameSite、Secure、HttpOnly；不得在 URL 传 bearer token。避免 iframe 任意嵌入和跨设备 cookie 污染。
验收：带前缀页面、REST、WS、会话到期、未授权拒绝、跨设备拒绝、内部地址不泄漏。当前阶段未接入任何现场 Web。
