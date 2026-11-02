# Phase1-A1.1-R2 从 Demo 到成熟平台能力路线图

## 1. 当前阶段画像

当前 A1.1 已经拥有成熟的认证、部署、安全和视觉基础，但业务层仍有明显 Demo 特征：项目和资产以只读浏览为主；首页偏项目/品牌展示；真实部署位置、Equipment 档案、项目排期、维保、历史回放等能力尚未形成闭环。

成熟化不是“再加更多卡片”，而是按顺序补齐：**可编辑主数据 → 工作流 → 时态历史 → 语义孪生 → 取证回放 → 生产治理**。

## 2. 五级成熟度

### Stage 0：Premium Demo-like（当前）

- OIDC/RBAC/Session/CSRF 已生产化；
- 首页、项目、设备、告警可查看；
- Three.js 行车模型具备四轴运动、自适应画质和后处理；
- 资产字段少、编辑入口少；
- 历史/维保/项目交付能力不足。

### Stage 1：Editable Industrial Workspace

目标：管理员能真正维护项目、资产和 Equipment，viewer 保持只读。

必须具备：
- 项目登记与正式项目区分；
- Asset/Location/Equipment/Catalog CRUD；
- 服务端分页/搜索/排序/筛选；
- 乐观并发、幂等、审计；
- Operations Map 与快速进入；
- 高密度 DataGrid、表单、DirtyGuard、409 冲突提示。

### Stage 2：Operational Platform

目标：平台能组织实际项目执行和设备运维。

必须具备：
- Project Portfolio、Milestone、资源日历、排期与冲突；
- 点检、PM、工单、钢丝绳、标定、软件/固件；
- Risk/Issue/Change、Commissioning、Acceptance、Punch、Handover；
- Attention Center 与到期提醒。

### Stage 3：Semantic Digital Twin

目标：3D 对象等于真实 Equipment，而不是“模型零件”。

必须具备：
- semantic_slot；
- Equipment Tree / Inspector；
- LiDAR FOV、Camera Frustum、标定坐标；
- Select/Focus/Isolate/Hide/X-Ray；
- System Topology；
- GLB version/accuracy/semantic manifest。

### Stage 4：Historical Operations & Forensics

目标：能回答“过去某时刻到底发生了什么”。

必须具备：
- entity_revisions；
- temporal installation/relation/binding；
- telemetry time quality/gap；
- Configuration@T；
- History Explorer；
- Three Replay；
- Incident Review。

### Stage 5：Mature Enterprise Platform

目标：Portfolio、资产、运维、历史、合规和治理形成闭环。

衡量标准：
- 业务人员不需要直接改 YAML/数据库；
- 项目施工冲突可提前发现；
- 设备替换后历史不丢；
- 任何重要变化都能审计；
- 3D/地图/历史不是展示，而是工作入口；
- 数据量扩大后仍有明确性能、容量和迁移策略。

## 3. 成熟化优先级

优先级从高到低：

1. 数据正确性与权限边界；
2. CRUD 与实际工作流；
3. 历史可追溯；
4. 信息架构与高密度 UI；
5. Three 语义化；
6. 高级视觉效果；
7. AI/自动优化等后续能力。

**原则**：不能先做炫酷 Twin，再补错误的数据模型；不能为了快加按钮，而绕开后端 capability 和审计。

## 4. 用户角色视角

### Portfolio Planner/Manager

关心：新项目、施工时间、资源冲突、延期、进场、验收、组合负荷。

### Project Platform Admin

关心：授权项目中的资产档案、位置、设备、目录、资料、退役、移交。

### Maintainer

关心：检查、工单、维护计划、标定、钢丝绳、部件状态、更换记录。

### Operator

关心：运行状态、告警、实时/历史、发起维护请求；不修改主资产。

### Viewer

只读浏览和分析；不能通过隐藏按钮之外的任何方式写入。

## 5. 关键产品原则

- **当前值与历史值并存**：不能只保留“现在”。
- **计划与 AS-BUILT 分开**：项目里计划安装什么，不等于现场最终装了什么。
- **业务状态与运行状态分开**：IN_SERVICE 不等于 ONLINE；UNKNOWN 是合法状态。
- **编辑与运行身份分开**：业务用户可改品牌型号，不能改真实 runtime identity。
- **示意与精确分开**：MANUAL pose/行政区中心不能标成真实精确位置。
- **提醒与法定结论分开**：软件提醒不等于法定检验。

