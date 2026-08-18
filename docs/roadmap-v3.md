# AILS HPC 维护期开发路线图(v3)

制定于 2026-08-18(v2 全量交付 + PR #63 分区管理之后)。用户选型已定:
**U 用户生命周期补全 + X 审计补齐 + P 作业模板预设**纳入本期;T API token 观望;
4.3 多物理节点继续挂起(无硬件计划)。验证基线沿用:
`go test ./...` 全绿 + `pnpm build` 绿 + 生产部署后按项 live 验证。

## 轨道 U:用户生命周期补全(优先——平台管理面残缺 + 运维死锁)

现状缺口:平台侧只有"建号"(users:create)——无用户目录、无禁用、无密码重置。
**tenant_admin 忘记密码时无人能重置**(重置端点只在租户侧,tenant_admin 只能重置本租户
member;无自助找回通道)。而 store 层已全部就绪(`UpdateUserStatus` 禁用即
token_version+1 在途令牌即刻失效;`ResetUserPassword` 强制首登改密+旧密入历史),
纯缺 API + UI 胶水。

| # | 内容 | 说明 | 规模 | 状态 |
|---|---|---|---|---|
| U1 | 平台用户目录 | GET /admin/users(跨租户全量,过滤 user/tenant/role;sqlite store 已实现 UserStore.ListUsers 面,补 handler+路由);admin 页平台面板用户表(角色改派复用 RoleAssignSelect、状态徽章、所属租户)——AssignPlatformRole API 已有但前端至今无入口 | M | **已完成**(PR #64) |
| U2 | 平台用户禁用/启用 | PATCH /admin/users/:username {status}——直通 store.UpdateUserStatus(禁用即吊销在途令牌;启用不回退版本,被吊销令牌不复活)。自禁用 400 守卫 | S | **已完成**(PR #64) |
| U3 | 平台密码重置 | POST /admin/users/:username/password——直通 store.ResetUserPassword(must_change_password=1,首登强制改密白名单中间件已有)。**消除 tenant_admin 忘密死锁**;弱密码 400(策略校验) | S | **已完成**(PR #64) |
| U4 | displayName 落地 | 补 store 方法;UpdateMyUser / 平台 PATCH 真正写显示名(service.go `_ = displayName` stub 转正——前端已在传、表格已在显示);userSelect/scanUser 贯通 display_name,平台/租户两侧均可编辑 | S | **已完成**(PR #64) |

权限点:新 `users:manage`(目录/状态/重置;`users:create` 保留只管建号——建号与
治理分权,可只授其一),词汇表 19→20,admin 独占。rbac-matrix.md + 矩阵三处测试同步。

## 轨道 X:审计补齐(与 U 同批收口)

| # | 内容 | 说明 | 规模 | 状态 |
|---|---|---|---|---|
| X1 | 预约/QOS 写操作落审计 | reservations.create/delete、qos.create、tenant.qos 照 partition.update 模式(service 层 WriteAudit;st 为 nil 的 yaml 模式跳过——与纯集群操作语义一致;租户侧重置密码同步补齐 user.reset_password) | S | **已完成**(PR #64) |

## 轨道 P:作业体验

| # | 内容 | 说明 | 规模 | 状态 |
|---|---|---|---|---|
| P1 | 提交模板预设 | 提交表单「常用模板」折叠区(单卡 PyTorch / 多卡 DDP / CPU 小任务等一键填充)。模板前端内置,不落库——零迁移零权限面;三个模板:CPU 小任务/单卡 PyTorch/长时批处理 | S | **已完成**(PR #64) |

## 轨道 T:程序化访问(观望——触发条件明确再启动)

| # | 内容 | 说明 | 规模 | 状态 |
|---|---|---|---|---|
| T1 | 个人 API token | 长期可撤销 token 供脚本提交作业(复用现有 REST 端点+审计)。**触发条件:出现真实的脚本化调度需求**(当前用户群以面板交互为主,登录 JWT 已够用) | M | 挂起 |

## 暂缓与明确不做

- **4.3 多物理节点**:继续挂起——provision 泛化是 L 级,无新硬件不动。
- 不做清单(评估过、明确裁掉,后续同类提议直接对照本表):
  - Slurm 配置面板编辑——slurm.conf 走 config-as-code(deploy 仓库),面板改配置绕过版本管理且高危
  - 独立文件管理器——Web-IDE(code-server/Jupyter)已覆盖文件浏览/上传
  - 邮件/webhook 通知——无 SMTP 基础设施,作业页 5s 轮询已够;真要也先做面板内提示
  - 用户删除——sacct 记账按 account 关联,删除破坏历史;禁用+留痕是 HPC 惯例
  - i18n / 主题切换 / 移动端深适配——单一中文实验室、桌面运维场景
  - 通用 API 限流——内网单 apiserver,登录防爆破已做(2.1)
  - Prometheus export——monitor 页已有,等真有 Grafana 需求

## 交付记录

- 2026-08-18:U/X/P 全量合入并部署生产(PR #64);部署后 verify_rbac.sh 抓到内置角色
  行与词汇表失同步(roles 表 is_system 行是迁移期快照,系统角色不可 API 改→无更新
  通道)——PR #65 修复:Open 每次 resyncBuiltinRoles 对齐 BuiltinRolePermissions,
  词汇表扩充自此结构性自愈。生产 verify 28/28;平台重置密码全链路(重置→首登强制
  改密)人工验收通过。T1 继续挂起,触发条件不变。

## 执行顺序

**U1(目录是 U2/U3 的 UI 载体)→ U2+U3+U4(一批)→ X1(顺手,同 PR 或紧随)→ P1(纯前端随时可插);T1 等触发**

依赖理由:U2/U3 的操作按钮挂在 U1 用户表行内;X1 与 U 同动 admin service,一批
改完一批测;P1 零后端依赖,可作穿插小件。

## 关键设计决策(先记下,实施时不再摇摆)

- **`users:manage` 与 `users:create` 分立**:建号与治理不同权——自定义角色可只授建号
  不给重置/禁用权;词汇表、BuiltinRolePermissions、前端回退表/标签四处同步(同
  partitions:manage 教义)。
- **平台重置一律 must_change_password=1**:复用 A1 机制,首登强制改密中间件白名单已
  就绪,不新增策略面。
- **禁用不回退 token_version**:沿用 store 既有语义——重新启用后旧令牌不复活。
- **U1 目录读权限并入 `users:manage`**:平台租户/成员查看已有 tenants:read 管租户维度;
  用户目录是治理入口,与状态/重置同权同授。
- **模板预设前端内置**:模板即代码(随 dist 发版);用户自定义/共享模板等真实诉求出现
  再表化,不预埋。
