# AILS HPC 维护期开发路线图(v2)

制定于 2026-08-17(多租户 0-6 + roadmap v1 轨道 1-4 完成后)。用户选型已定:
**RBAC 升级为自定义角色** + **SSO 走 OIDC/OAuth2**。验证基线沿用:
`go test ./...` 全绿 + `pnpm build` 绿 + 生产部署后按项 live 验证。

## 轨道 R:RBAC 自定义角色(地基——A/B 轨都依赖它)

| # | 内容 | 规模 | 状态 |
|---|---|---|---|
| R1 | **权限点清单与常量**:把现有四角色×路由矩阵翻译成权限点集合(如 `jobs:submit`、`nodes:drain`、`billing:read:all`、`tenant:users:manage` 等,约 15-20 个);`RequireRole(...)` 全部替换为 `RequirePermission("...")`,内部暂由内置角色映射满足(行为零变化,测试全绿为证) | M | 待做 |
| R2 | **角色表化**:新表 `roles(name, permissions JSON, is_system, tenant_id NULL=平台内置)` + `users.role_id`(保留 role 字符串列做迁移兼容);四内置角色(admin/ops_admin/tenant_admin/member)seed 成系统角色;迁移 = 把现有 users.role 字符串映射到 role_id | M | 待做 |
| R3 | **角色管理 API**:admin 管平台角色+查看各租户自定义角色;tenant_admin 在本租户内建角色(权限只能是自身权限子集——不可提权)、分配给用户 | M | 待做 |
| R4 | 权限自描述 `GET /auth/me`(角色+权限清单+clusterUser/tenant);**前端全面能力驱动**:菜单/按钮/路由守卫按权限渲染,删除所有 `role === 'admin'` 硬编码 | M | 待做 |
| R5 | 越权对抗测试:自定义角色提权(给超出父集的权限→400)、跨租户角色读写→404、角色删除时在用用户的处置 | S | 待做 |

## 轨道 S:OIDC/OAuth2 SSO

| # | 内容 | 规模 | 状态 |
|---|---|---|---|
| S1 | **OIDC 授权码流**:`/auth/oidc/login`(拼 authorize URL,state+PKCE)→ IdP → `/auth/oidc/callback`(换 token+验签,可选 userinfo)→ 命中本地用户则签发门户 JWT,未命中按配置决定 JIT 开户或拒绝;`auth_source='oidc'`。配置走 env(`AILS_OIDC_ISSUER/CLIENT_ID/CLIENT_SECRET/REDIRECT`) | M | 待做 |
| S2 | **角色/租户映射**:OIDC claim(如 `roles`/`groups`)→ 角色名/租户 slug 映射表(env 或表配置);无映射 claim 的用户默认最小角色或拒绝(可配) | S | 待做 |
| S3 | 前端:登录页「SSO 登录」按钮(有 OIDC 配置才显示,由后端 public 配置端点告知)+ 回调路由(/login/oidc/callback 收 code 换会话) | S | 待做 |
| S4 | 账号关联:已登录本地账号绑定/解绑 SMO sub;SSO 首登撞用户名时的确认流 | S | 待做 |

## 轨道 A:策略与审计(在 R 之上)

| # | 内容 | 规模 | 状态 |
|---|---|---|---|
| A1 | 密码与会话策略:复杂度(大小写/数字/符号)、历史 N 次不可重用(新表 `password_history`)、首次登录强制改密(`must_change_password` 列)、会话列表+全设备登出 UI(token_version 已有) | M | 待做 |
| A2 | 审计补全:登录成功/失败、作业提交与控制、IDE 操作入库(audit_log 现只覆盖 admin 变更) | S | 待做 |
| A3 | 权限矩阵文档化(docs/rbac-matrix.md,与 router 测试对照)+ 矩阵测试扩展到自定义角色 | S | 待做 |

## 轨道 C:数据库维护(独立,先行)

| # | 内容 | 规模 | 状态 |
|---|---|---|---|
| C1 | **启用外键执行**:DSN 加 `_pragma=foreign_keys(1)`;迁移 v2 先清存量孤儿 | S | **已完成**(PR #57,prod 验证:migrations=[1,2]、孤儿=0) |
| C2 | 索引/触发器:idx_audit_actor + trg_users/tenants_updated_at | S | **已完成**(PR #57) |
| C3 | schema 文档化 docs/db-schema.md(ERD+每表每列说明,与 migrate.go 对照) | S | 待做 |
| C4 | 备份恢复演练:真实恢复一次用户库+文档化 runbook | S | 待做 |

## 执行顺序

**C1+C2(清地基,半天)→ R1(权限点,行为零变化)→ R2+R3(角色表化+管理)→ R4(能力驱动)→ R5 → A2 → S1+S2+S3(SSO)→ S4 → A1 → A3 → C3+C4**

依赖理由:C 先行因为 R2 要加表,先把 FK/索引基础打好;R1 是零行为变化的重构,先落权限词汇表;S 依赖 R(JIT 开户要分配角色,R3 之前只能映射到内置角色)。

## 关键设计决策(先记下,实施时不再摇摆)

- **权限点为权威,角色是权限的命名集合**:路由/前端只认权限点,不认角色名——自定义角色才不可提权越界。
- **内置四角色不可删改**(is_system),保证升级路径平滑与默认行为不变。
- **tenant 自定义角色的权限必须是创建者权限的子集**(服务端取交集校验)。
- OIDC 与本地密码**并行**(auth_source 区分),不做纯 SSO 强制,除非配置声明。
- claims 兼容:现有 JWT(角色字符串)在过渡期继续有效,`VerifyToken` 后按 role_id 二次解析——迁移窗口一个 release。
