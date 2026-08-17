# AILS HPC RBAC 权限矩阵

权威实现：`pkg/auth/permissions.go`（权限点词汇表与内置角色映射）、
`cmd/apiserver/router.go`（路由 → 权限点装配）。
对照测试：`pkg/auth/permissions_test.go`（内置矩阵等价性）、
`cmd/apiserver/router_test.go`（四角色×路由矩阵）、
`cmd/apiserver/rbac_adversarial_test.go`（自定义角色执行/越权）。
**改矩阵必须同步本文档与三处测试。**

## 1. 权限点词汇表（18 项）

| 权限点 | 语义 | 典型持有者 |
|---|---|---|
| `cluster:read` | 读集群状态（ping/nodes/jobs/detail/history/partitions/monitor） | 所有角色 |
| `nodes:manage` | 节点 DRAIN/RESUME | admin |
| `jobs:submit` | 作业提交 | member, tenant_admin |
| `jobs:control` | 作业取消/挂起/重排 | member, tenant_admin |
| `ide:list` | Web-IDE 会话列表 | member, tenant_admin |
| `ide:manage` | Web-IDE 启动/回收/延时/反代 | member, tenant_admin |
| `billing:read` | 计费读取（数据范围见 §4） | member, tenant_admin, ops_admin |
| `tenants:read` | 平台租户清单/租户成员查看 | admin |
| `tenants:manage` | 平台租户创建/修改/QOS 绑定 | admin |
| `users:create` | 平台用户创建 | admin |
| `audit:read` | 平台审计日志查看 | admin |
| `reservations:manage` | 预约查看/创建/删除 | admin |
| `qos:manage` | QOS 查看/创建/绑定 | admin |
| `roles:manage` | 平台自定义角色 CRUD + 指派 | admin |
| `tenant:users:read` | 本租户成员查看 | tenant_admin |
| `tenant:users:manage` | 本租户成员创建/修改/角色改派 | tenant_admin |
| `tenant:users:reset_password` | 本租户成员密码重置 | tenant_admin |
| `tenant:roles:manage` | 本租户自定义角色 CRUD | tenant_admin |

## 2. 内置角色 → 权限点矩阵

| 权限点 | admin | ops_admin | tenant_admin | member |
|---|:-:|:-:|:-:|:-:|
| cluster:read | ✓ | ✓ | ✓ | ✓ |
| nodes:manage | ✓ | | | |
| jobs:submit | | | ✓ | ✓ |
| jobs:control | | | ✓ | ✓ |
| ide:list | | | ✓ | ✓ |
| ide:manage | | | ✓ | ✓ |
| billing:read | | ✓ | ✓ | ✓ |
| tenants:read | ✓ | | | |
| tenants:manage | ✓ | | | |
| users:create | ✓ | | | |
| audit:read | ✓ | | | |
| reservations:manage | ✓ | | | |
| qos:manage | ✓ | | | |
| roles:manage | ✓ | | | |
| tenant:users:read | | | ✓ | |
| tenant:users:manage | | | ✓ | |
| tenant:users:reset_password | | | ✓ | |
| tenant:roles:manage | | | ✓ | |

要点：admin 是纯硬件/平台监控管理角色——**不含** jobs/ide/billing（历史矩阵如此，
R1 等价性测试锁定）；ops_admin 是计费观测角色；作业与 IDE 归租户侧角色。

## 3. 路由 → 权限点（与 router.go 一一对应）

| 路由 | 权限点 |
|---|---|
| GET /slurm/ping·nodes·jobs·jobs/:id/detail·jobs/history·partitions·monitor/* | cluster:read |
| POST /slurm/nodes/:name/state | nodes:manage |
| POST /slurm/jobs/submit | jobs:submit |
| POST /slurm/jobs/:id/cancel·hold·requeue | jobs:control |
| POST /slurm/containers/launch · DELETE /slurm/containers/:id · POST /slurm/containers/:id/extend · ANY /ide/:session/* | ide:manage |
| GET /slurm/containers/list | ide:list |
| GET /slurm/billing/usage·export | billing:read |
| GET /admin/tenants · /admin/tenants/:slug/users | tenants:read |
| POST /admin/tenants · PATCH /admin/tenants/:slug | tenants:manage |
| POST /admin/users | users:create |
| GET /admin/audit | audit:read |
| GET·POST·DELETE /admin/reservations* | reservations:manage |
| GET·POST /admin/qos · PATCH /admin/tenants/:slug/qos | qos:manage |
| GET·POST /admin/roles · PATCH·DELETE /admin/roles/:name · GET /admin/tenants/:slug/roles · PATCH /admin/users/:username/role | roles:manage |
| GET /tenants/me/users | tenant:users:read |
| POST /tenants/me/users · PATCH /tenants/me/users/:username | tenant:users:manage |
| POST /tenants/me/users/:username/password | tenant:users:reset_password |
| PATCH /tenants/me/users/:username/role | tenant:users:manage |
| GET·POST /tenants/me/roles · PATCH·DELETE /tenants/me/roles/:name | tenant:roles:manage |
| POST /auth/password · GET /auth/me(+sessions) · POST /auth/logout-all · /auth/oidc/** | 仅认证（无权限点） |

## 4. 数据可见范围（scope，按基角色推导，独立于权限点）

| 基角色 | scope | 作业/IDE/计费可见面 |
|---|---|---|
| admin / ops_admin | all | 全平台 |
| tenant_admin | tenant | 本租户成员（成员清单过滤） |
| member（及一切自定义基角色） | self | 仅本人 |

控制类越权（非属主 cancel 等）另有属主判定：member 仅本人作业；tenant_admin 本租户
（root 通道）；跨租户一律 403（`TestRouter_TenantScoping` 锁定）。

## 5. 自定义角色规则（R2/R3）

- 角色行：`roles(name, permissions JSON, base_role, is_system, tenant_id NULL=平台)`；
  `users.role_id` 指向，`users.role` 恒存基角色（scope 推导用）。
- **权限点是权威**：路由/前端只认权限点；角色是权限的命名集合。
- 内置四角色 `is_system=1` 不可删改（409）。
- 租户自定义角色权限 **必须是创建者权限的子集**（服务端 `ensureSubset` 校验，越界
  400；更新放大同样拦截——角色链归纳不可提权）。
- base_role 作用域：平台角色 ∈ {admin, ops_admin}；租户角色 ∈ {member, tenant_admin}。
- 指派归属：系统角色按基角色判租户（§2.3）；平台自定义角色限 system 租户；租户
  角色限本租户。跨租户读写统一 404（防枚举）。
- 删除处置：在用角色 409（须先改派）；外键为 DB 层兜底。
- 改派/权限调整**即刻生效**（中间件每请求按库刷新 claims，无需重登、无需吊销）。
- 伪造防护：token 内的 perms 声明不具权威性——带库中间件一律覆写。

## 6. 前端能力驱动（R4）

- 数据源：登录响应与 `GET /auth/me` 的 `permissions[]`；`can(perm)` 助手
  （`apps/web/app/services/auth.ts`）。
- 菜单/按钮/路由守卫按权限渲染；后端 RequirePermission 是权威门（前端隐藏只是体验）。
- 旧 localStorage 无 permissions 时回退内置映射表（与后端同步维护）。
