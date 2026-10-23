# Phase 1-A0 开发资产模式

本轮只交付代码与CI，服务器部署、删除实验服务器旧数据须另行人工审查批准。下列命令是后续获批后的开发操作入口，不表示已在服务器执行。

## 本地入口

先运行node tools/task.mjs bootstrap，生成忽略目录中的开发凭据、证书和默认legacy ACL。node tools/task.mjs dev-up仍为旧Count模式，供原20台回归使用；node tools/task.mjs asset-up显式启用资产模式。首次切换前停止旧服务，不同时运行两套同项目Compose配置。

asset-up按INVENTORY_FILE选择输入；未设置时优先使用本地私有Inventory，不存在时使用匿名example。它把Inventory复制到忽略的runtime目录，派生仅含身份关系的10台Fleet，生成逐设备tenant/project/device开发ACL，并在现有开发CA下签发清单中的叶证书。证书与ACL都不写入公开源码。

Compose资产overlay仅只读挂载inventory.json和fleet.json两个指定文件，不挂载整个私有目录或CA私钥。Cloud显式导入后连接清单Topic，Simulator使用--fleet-file。此配置不会自动删除旧数据库历史；资产模式API只展示当前清单。需要停止时运行node tools/task.mjs dev-down，保留数据卷。

## 工具

```sh
go run ./tools/inventory validate <private-file>
go run ./tools/inventory import <private-file>
go run ./tools/fleet --inventory-file <private-file> --out <runtime-acl> --manifest-out <fleet-file>
go run ./tools/certs --fleet-file <fleet-file>
```

import通过环境DATABASE_URL连接PostgreSQL；不要把连接字符串或凭据写入命令历史、文档或聊天。工具只做既有库的幂等upsert，迁移由Cloud的--migrate入口应用。Manifest优先于--count，传入不存在的文件会拒绝启动，不会悄悄回退成20台。旧Count默认仍由专用legacy描述符适配，Runner/Mapper不硬编码项目。

开发ACL按每个证书CN逐项列出Topic，Cloud也只获得当前清单的确切Topic。SIM-P001-01不能发布到P004，即使deviceId不变。该ACL不是生产PKI或正式设备注册系统；未来真实Edge必须申请独立EDGE身份和证书。

## 获批后切换实验服务器的步骤

1. 核验功能分支最终CI和SHA，保持服务器main目录不变，在隔离工作目录准备配置。
2. pg_dump保存当前实验库，验证备份可读，记录本地保存位置；备份不得上传Git。
3. 停止旧Simulator与Cloud写入，再运行受保护的旧20台清理工具。
4. 导入私有Inventory，生成开发Fleet/ACL/证书；核对9个登记项目、10个资产、10个SIMULATION、0个REAL。三个已完成项目保持0资产和0运行端。
5. 启动资产overlay，验证10个确切运行ID、跨项目ACL、Watch/LIVE及UI来源语义。只按行政区显示位置。

清理入口为 `APP_ENV=development` 环境下的 `go run ./tools/inventory reset-legacy-simulator-fleet --confirm-legacy-20`。它只允许SIM-001至SIM-020且source_mode=SIMULATION、未绑定资产的身份；任何同名身份已有非legacy来源或资产关联都会整次拒绝。生产环境拒绝执行，不接受任意ID列表或DELETE ALL。TEST-001和SIM-021不在删除范围，历史审计保留；若实验环境仍有其他测试身份，应人工单独审查，不得扩大清理脚本。

回滚先停止新运行端，用备份恢复到独立数据库并恢复原Count配置/开发ACL；不在带新资产记录的库中擅自逆向迁移，不复用SIM证书给真实设备。配置和数据库恢复必须经服务器阶段人工确认。
