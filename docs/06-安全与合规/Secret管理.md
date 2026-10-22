# Secret 管理

bootstrap 生成随机 DB password、DEV_API_TOKEN 和开发 CA/每设备私钥，位于 deploy/compose/runtime，已忽略。工具/缓存/本地客户归档均不进 Docker context 或 Git。
开发 CA 30 天，叶证书 7 天；过期后停止环境、备份必要数据，再删除本地生成证书目录并 bootstrap 重新生成。不可混用旧 CA 与新叶证书。生产密钥通过受控 secret store 注入，不复制开发 CA。
浏览器只输入一次本地 token，HttpOnly SameSite cookie；不保存 localStorage、不放 URL，不记录 token/密码/原始请求头。凭据泄漏必须撤销轮换，Git 删除不等于撤销。
提交前 repository precheck + gitleaks；依赖 npm audit/go vulnerability/trivy。开发安全例外需逐项标识，不得全局关闭漏洞检查。
