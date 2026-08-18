# AILS HPC 运营期路线图(v4)

制定于 2026-08-18(v1-v3 三份路线图全量交付、生产 28/28 验收闭卷之后)。用户选型:
**W1 费率配置化 + W2 审计保留 + W3 配额可见性 + W4 E2E 扩面** + 触发驱动表正式入库。
定位说明:面板对当前规模(单集群、几十~几百用户)已功能完备——v4 无大轨道,
是运营小件批次 + 触发驱动的储备表;不预先开发未触发的项。
验证基线沿用:`go test ./...` 全绿 + `pnpm build` 绿 + 生产部署后按项 live 验证。

## 轨道 W:运营小件

| # | 内容 | 说明 | 规模 | 状态 |
|---|---|---|---|---|
| W1 | 费率配置化 | rateCPU/MEM/GPU 三常数(service.go 硬编码,注释自认"后续可移至 config")→ env `AILS_RATE_CPU/MEM/GPU`(缺省回落现值,零配置行为不变,同 AILS_OIDC_* 教义);billing 页透出当前费率与估算费用(计价透明) | S | **已完成**(本 PR) |
| W2 | 审计保留 + 查看器润色 | audit_log 只进不出无上限:保留窗口默认 365 天(env `AILS_AUDIT_RETENTION_DAYS` 可调),开库 prune(照 resyncBuiltinRoles 教义)+ 常驻每日 ticker;顺带审计查看器补 action 过滤下拉(API 已支持 ?action=,UI 只有 actor 框)。**实施发现**:预约/QOS/审计三面板的 JSX 自 v1 起从未落地(只有 state/loader)——本次补建全部三面板 | S | **已完成**(本 PR) |
| W3 | 租户配额可见性 | 成员/租户管理员可见本租户 GrpTRES 限额 vs 已用量对比:限额经 sacctmgr show assoc(父账号)封装只读查询;用量复用 billing 聚合;展示挂 billing 页顶部(scope 内:member/tenant_admin 看本租户,admin 全部);权限点复用 billing:read。**实施调整**:admin 不持 billing:read(纯硬件监控教义)→双入口:/slurm/billing/quota(billing:read,scope 收口)+/admin/tenants/quotas(tenants:read 平台总览);对比语义修正为并发上限展示(GrpTRES=并发上限非累计用量) | S-M | **已完成**(本 PR) |
| W4 | E2E 盘点扩面 | TEST_INFRA 的 feature inventory 从四大核心需求扩到 v2/v3 面:RBAC 权限门、用户生命周期(禁用/重置/displayName)、分区管理、预约/QOS、审计写入;live 模式打本地 docker 集群不打生产;与 go test 矩阵互补(HTTP 契约 + 部署形态层)。**实施发现**:TEST_INFRA 描述的 test/e2e 影子套件从未入库→不重建(平行实现反模式),改为:真实路由上的运维旅程链测试(TestE2E_OperationalJourneys,三链覆盖 v2/v3/v4)+ TEST_INFRA/TEST_READY 重写为真实架构 | M | **已完成**(本 PR) |

## 触发驱动表(触发即启动,不预先开发)

| 触发项 | 触发条件 | 触发后动作 | 预估 |
|---|---|---|---|
| T1 个人 API token(自 v3 挂起) | 有用户开始写脚本/cron 调面板 API 提交作业 | 长期可撤销 token 表 + 签发/吊销端点 + 审计;复用现有 REST 面与密码学设施 | M |
| 4.3 多物理节点(自 v1 挂起) | 有新硬件接入计划 | provision 泛化(节点注册/标签/分区拓扑发现);部署文档先行 | L |
| OIDC 生产启用 | 选定 IdP(校园统一认证/Keycloak 等) | 纯配置+运维:AILS_OIDC_* env、角色/租户映射表、绑定流验收(S1-S4 代码已就绪) | S(配置) |
| Slurm 升级适配 | 集群从 21.08 升级 | slurmrestd API 版本 v0.0.37 硬编码面(路径前缀/响应 schema)适配 + 回归 | M |

## 暂缓与明确不做

不做清单沿用 docs/roadmap-v3.md(Slurm 配置面板/文件管理器/邮件通知/用户删除/
i18n/限流/Prometheus 等),v4 无新增裁项;面板内帮助页暂缓(用户问再议,
walkthrough.md 在 repo 内可查)。

## 执行顺序

**W1 → W2 → W3 → W4(收官回归)**

理由:W1/W2 是即刻真实痛点且互不依赖可并行;W3 依赖 sacctmgr 只读封装先行设计;
W4 纯测试面放最后,一次把 W1-W3 的新端点纳入盘点。

## 关键设计决策(先记下,实施时不再摇摆)

- **费率是策略不是代码**:env 覆盖 + 代码缺省回落——改价改 env 重启即生效,
  不发版;billing 页展示当前生效费率,计价透明不藏。
- **审计裁剪幂等且保守**:开库 + 每日各一次 DELETE(created_at 索引已有 idx_audit_actor
  覆盖不了——按需补 created_at 索引);默认 365 天宁长勿短,审计是安全数据,
  误删不可恢复(无备份外的恢复通道),窗口只放大不缩小。
- **配额读数走 Slurm 权威**(sacctmgr),不从 DB 推断——限额可能被集群侧直接改过,
  DB 里的租户限额字段只是设置时的快照。
- **E2E 不打生产**:live 模式目标=本地 docker compose 集群;生产验证仍走
  verify_rbac.sh(无副作用用例集)。
