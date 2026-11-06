# G3-1 高保真原型（DESIGN_ONLY）

单文件 `index.html`，无外部依赖，浏览器直接打开。`?screen=<名称>` 切换画面；窗口宽度 ≤700px 时自动切换为手机版式。所有数字、项目、人员均为合成数据，页面顶部固定显示 `DESIGN_ONLY · 合成数据 · 非平台真实统计`，**不代表平台已实现的功能或统计**。

| screen | 画面 | 角色 | 依赖的真实/拟议接口 |
| --- | --- | --- | --- |
| `sales` | 销售工作台 | 销售 | 现有：workspace-summary、projects；拟议：阶段分布（G3-3） |
| `manager` | 主管工作台 | 主管 | 现有：workspace-summary；拟议：reviews?eligibility（G3-2）、团队负载（G3-4） |
| `director` | 总监工作台 | 总监 | 拟议：stage-summary 与金额（G3-3）、跨组重复队列（G3-2）、团队对比（G3-3/G3-4） |
| `admin` | 管理员工作台 | 管理员 | 现有：workspace-summary、assignment=unassigned、staff；拟议：人员异常聚合 |
| `projects` | 项目中心（表格/手机卡片） | 主管 | 现有：projects；拟议：负责人筛选、下一步状态 |
| `pipeline` | 销售管道（全量列计数、逐栏分页） | 总监 | 拟议：stage-summary 与逐栏分页（G3-3） |
| `review` | 审核工作区（可审/需他人/全部） | 主管 | 拟议：reviews?eligibility（G3-2） |
| `compare` | 重复对比（含跨组受限提示） | 主管 | 现有：duplicate-comparison；按 26 号第 6 节白名单脱敏（G3-2） |
| `detail` | 项目详情（阶段路径、下一步、待补） | 销售 | 现有：projects/{id}；拟议：阶段待补字段规则 |
| `create` | 新建项目（含归属与查重侧栏） | 管理员 | 现有：projects、duplicate-checks、ownership-candidates |

截图（桌面 1440 与手机 390 各 10 张）在忽略目录 `local-private/baobei/g31-evidence/design/`。
