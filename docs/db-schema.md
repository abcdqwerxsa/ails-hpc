# AILS HPC 用户库 Schema 文档（sqlite）

权威实现：`pkg/store/migrate.go`（`schemaMigrations` v1–v5，Open 时按序幂等应用）。
本文档与其逐列对照——**改表必须两处同步**。库文件默认 `var/lib/ails/ails.db`
（`AILS_DB_PATH`），WAL + busy_timeout(5s) + `foreign_keys(1)`（C1 起强制执行）。

## ERD

```
┌──────────────┐        ┌──────────────────┐
│   tenants    │1      *│      users       │
│──────────────│────────│──────────────────│
│ id PK        │        │ id PK            │
│ slug UNIQUE  │        │ tenant_id FK ────┘
│ name         │        │ role (基角色)     │
│ parent_account UNIQUE │ role_id FK ──┐
│ status       │        │ username U   │  │
│ created_at   │        │ password_hash│  │
│ updated_at ▸ │        │ cluster_user U  │
└──────────────┘        │ uid U / gid  │  │
                        │ account U    │  │
                        │ display_name │  │
                        │ email        │  │
                        │ auth_source  │  │
                        │ oidc_sub (部分U) │
                        │ status       │  │
                        │ token_version│  │
                        │ must_change_password
                        │ created_at / updated_at ▸
                        └──────────────┼──┘
                                       │
┌──────────────┐        ┌──────────────▼──┐
│ audit_log    │        │      roles      │
│──────────────│        │─────────────────│
│ id PK        │        │ id PK           │
│ actor        │        │ name            │
│ action       │        │ description     │
│ target       │        │ permissions JSON│
│ detail JSON  │        │ base_role       │
│ request_id   │        │ is_system       │
│ created_at   │        │ tenant_id FK→tenants (NULL=平台)
└──────────────┘        │ created_at / updated_at ▸
                        └─────────────────┘
┌──────────────────┐    ┌──────────────────┐
│ password_history │    │     sessions     │
│──────────────────│    │──────────────────│
│ id PK            │    │ id PK            │
│ username         │    │ username         │
│ password_hash    │    │ issued_at        │
│ changed_at       │    │ expires_at       │
└──────────────────┘    │ ip / user_agent  │
                        │ token_version    │
┌──────────────────┐    └──────────────────┘
│schema_migrations │    索引：idx_users_tenant / idx_audit_actor /
│──────────────────│    idx_roles_{platform,tenant}_name(部分唯一) /
│ version PK       │    idx_users_oidc_sub(部分唯一) /
│ applied_at       │    idx_password_history_user / idx_sessions_user
└──────────────────┘    触发器：trg_{users,tenants,roles}_updated_at
```

## tenants（v1）—— 租户

| 列 | 类型/约束 | 说明 |
|---|---|---|
| id | INTEGER PK AUTOINCREMENT | |
| slug | TEXT UNIQUE | 租户标识；`system` 为保留租户（admin/ops_admin 归属，API 不可建/不可挂起） |
| name | TEXT | 显示名（缺省=slug） |
| parent_account | TEXT UNIQUE | Slurm 父账号（fairshare 层级载体，建租户时 =slug） |
| status | CHECK active\|suspended | suspended 时不可新增用户 |
| created_at / updated_at | TEXT | updated_at 由 trg_tenants_updated_at 触发器维护 |

## users（v1 建表；v3 +role_id；v4 重建扩 auth_source/oidc_sub；v5 +must_change_password）

| 列 | 类型/约束 | 说明 |
|---|---|---|
| id | INTEGER PK | |
| username | TEXT UNIQUE | `^[a-z_][a-z0-9_-]{0,31}$` |
| password_hash | TEXT | bcrypt（OIDC 账号为 32B 随机值哈希——本地不可登录） |
| role | CHECK 四常量 | **基角色**（scope 推导：admin/ops=all、tenant_admin=tenant、member=self）；自定义角色时代与 role_id 并存 |
| role_id | FK→roles(id) | 实际角色（R2 起）；users.role 恒存基角色做迁移兼容 |
| tenant_id | FK→tenants(id) NOT NULL | 归属租户（外键 C1 起强制） |
| cluster_user | TEXT UNIQUE | 集群 unix 身份（L1 隔离；作业 owner） |
| uid | INTEGER UNIQUE | 平台带宽 2001..2999（NextUID=max+1） |
| gid | INTEGER | 默认 2000 |
| account | TEXT UNIQUE | Slurm 账号（约定 =cluster_user） |
| display_name / email | TEXT | |
| auth_source | CHECK local\|ldap\|oidc | 凭证来源；绑定 SSO 不改来源（密码并行） |
| oidc_sub | TEXT（部分唯一索引） | IdP sub；NULL=未绑定。一个 sub 只可绑一个账号 |
| status | CHECK active\|disabled | disabled + token_version+1 即踢出在途令牌 |
| token_version | INTEGER | 改密/禁用/全设备登出 +1 → 在途 JWT 即刻失效（中间件逐请求比对） |
| must_change_password | INTEGER (bool) | 首登/被重置后强制改密门（A1；中间件只放行自助面） |
| created_at / updated_at | TEXT | 触发器维护 |

## roles（v3）—— 角色（R2 角色表化）

| 列 | 类型/约束 | 说明 |
|---|---|---|
| id | INTEGER PK | |
| name | TEXT | 作用域内唯一：平台（tenant_id IS NULL，部分唯一索引）/ 租户内（UNIQUE(tenant_id,name)）；不可与内置四角色重名（服务层保留） |
| description | TEXT | |
| permissions | TEXT (JSON array) | 权限点清单（词汇表见 docs/rbac-matrix.md）；创建时按 BuiltinRolePermissions 快照 seed，运行时库内值为权威 |
| base_role | CHECK 四常量 | scope 基角色；平台角色 ∈{admin,ops_admin}、租户角色 ∈{member,tenant_admin} |
| is_system | INTEGER (bool) | 内置四角色 seed=1，不可删改（409） |
| tenant_id | FK→tenants(id) NULL | NULL=平台角色；非空=租户自定义角色 |
| created_at / updated_at | TEXT | 触发器维护 |

## audit_log（v1；C2 加 idx_audit_actor）

| 列 | 说明 |
|---|---|
| id / actor / action / target | actor=操作者（SSO 未命中时 sub 前缀）；action 见下表；target=user:*/tenant:*/role:*/作业或节点 id |
| detail | JSON 文本（ip/状态码/锁定态等），缺省 {} |
| request_id | 贯穿中间件请求号 |
| created_at | |

action 清单：管理面 `tenant.create/update`、`user.create/update/role`、`role.create/update/delete`、`user.oidc.link/unlink/provision`；认证面 `auth.login/.login.fail/.login.locked/.password.change/.logout_all/.oidc.*`；作业与 IDE `jobs.submit/cancel/hold/requeue`、`ide.launch/recycle/extend`、`nodes.state`（A2 中间件按路由模式落）。

## password_history（v5）—— 密码历史（A1）

| 列 | 说明 |
|---|---|
| id / username / password_hash / changed_at | 每用户保留最近 5 条（裁剪 DELETE NOT IN top5）；自助改密/管理员重置前 bcrypt 逐条比对，命中 → 拒绝 |

## sessions（v5）—— 会话台账（A1）

| 列 | 说明 |
|---|---|
| id / username / issued_at / expires_at / ip / user_agent | 登录成功时记录（含 OIDC 签发） |
| token_version | 签发时版本；与 users.token_version 不一致或已过期 = 失效（改密/登出全部设备即整批吊销，无需逐条更新） |

## schema_migrations

| version PK / applied_at | 已应用版本号；Open 时按 `schemaMigrations` 下标补齐（每版本单事务）。当前最新 v5。 |

## 迁移历史

| 版本 | 内容 |
|---|---|
| v1 | 多租户三表（tenants/users/audit_log） |
| v2 | C1/C2：存量孤儿清洗 + 外键执行前置；idx_audit_actor；users/tenants updated_at 触发器 |
| v3 | R2：roles 表 + 四内置角色 seed（构建期权限快照）+ users.role_id 回填 + roles 触发器 |
| v4 | S1：users 重建（auth_source 扩 'oidc' + oidc_sub 部分唯一索引）——sqlite 不能改列约束，标准重建流程保 id/时间戳/role_id |
| v5 | A1：password_history + sessions + users.must_change_password |

## 运维备注

- 备份：`apiserver -backup-db <path>`（VACUUM INTO 在线快照；runbook 见 docs/db-restore-runbook.md）。
- 单写者约束：apiserver 是唯一写进程（DSN 串行化连接）；恢复演练时先停服务。
- 外键开启后 `roles`/`tenants` 删除会被引用阻塞——在用角色/租户的删除须先改派/清空成员（服务层已给 409 语义，DB 为兜底）。
