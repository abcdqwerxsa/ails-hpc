# AILS HPC 真·多租户 — 实施设计

状态:**已全部实施完毕(Phase 0-6,2026-08-14~17,PR #27~#35)**,生产运行中。
- 实施差异:Phase 2 生产验证揪出"改密分裂态"(已改全有或全无,#30);
  Phase 5 限额执法需 `AccountingStorageEnforce=associations,limits` 且
  limits 标志由 ctld 启动时读取(改后需重启 ctld,#35);
  Claims 的 orgSlug/tenantNs **保留未删**(迁就 24h 在途令牌,零成本容忍)。
- 全新装机 bootstrap:sqlite 库为唯一运行库——先写一个含初始 admin 的
  users.yaml → `apiserver -import-users config/users.yaml` → 用该 admin 登录
  → 其余租户/用户全部走管理 API(建租户自动建 Slurm 父账号)。
范围:租户为一等实体、sqlite 用户库、租户级读隔离、租户级 Slurm fairshare、users.yaml 零停机迁移。LDAP/SSO 暂出范围但留好接口缝。

> ⚠️ **Phase 0 的动因(现存漏洞)**:`pkg/services/billing/handler.go` 的 `GetUsage` 接受任意
> `?user=` 且**没有任何基于 claims 的过滤**——member token 当前可读任意用户的账单。Phase 0 关闭。

## 1. 目标

1. **租户一等**:每个用户恰好属于一个租户;tenant_admin 管理本租户用户;作业/会话/计费读取按租户策略隔离(不信任客户端 query 参数)。
2. **用户库入 DB**(sqlite)替代 `config/users.yaml`:bcrypt 不变,tenant_admin 的 CRUD API(建/禁用用户、重置密码),admin 管租户,审计日志。
3. **IdP 缝**:小接口,LDAAP/SSO 未来替换口令校验而无需改 handler。
4. **Slurm 映射**:保持每用户 `account == clusterUser`(L1/L3 与作业归属的基础),增加**租户父账号**使 fairshare/限额成为租户级;作业仍计入叶子账号,提交路径零变化。
5. **不破坏**:`router_test.go` 角色矩阵保持绿(只增不改);4 个开发账号可用;per-user JWT 铸造不动。

### 现状缺口(设计所修,已在代码核实)

- `LoginRequest.OrgSlug` 解析但未消费(方向正确;前端下拉可移除)。
- **billing `?user=` 无鉴权过滤(漏洞)**。
- `forbidIfNotOwner` / `forbidIfNotSessionOwner` 只约束 member;tenant_admin 是通配(可控制任何租户的作业/会话)。
- `UserStore` 只读,无 CRUD。

## 2. 关键决策

### 2.1 sqlite(modernc.org/sqlite,纯 Go),不用现有 MariaDB

MariaDB 属于 slurmdbd(记账专用);把平台用户库放进去 = 门户可用性耦合集群 DB、slurm 运维方意外获得 auth 写权限——**信任域错误**。规模为几十~几百用户、单写者(apiserver 单二进制),sqlite WAL 足够;零新容器、零网络。全部走 `database/sql` + store 接口,后续迁 MariaDB/PG 只是驱动+DSN。

DSN:`?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)`;库文件 `var/lib/ails/ails.db`。

### 2.2 Slurm 账号:租户父账号 + 每用户叶子(方案 B,推荐)

- 方案 A(现状扁平):全部挂 root 下;无租户 fairshare/限额。
- **方案 B**:`sacctmgr add account <tenant>` 为父,每用户账号 `parent=<tenant-account>`。**用户关联与提交仍在每用户叶子账号**——sacct 行、`JobOwner`、`forbidIfNotOwner`、billing breakdown 全不变;变的是 fairshare 树 + 可设租户级限额(`GrpTRES`/`MaxTRES`)。
- 迁移:`add account hpc-lab` → `modify account <user-acct> set parent=hpc-lab`(Slurm 23.02 验证;若 `modify parent` 不支持则维护窗内 remove+re-add,DB 已是真相源,关联可重建)。作业历史不受影响(sacct 记叶子账号)。

### 2.3 角色与"恰好一个租户"

保留租户 `slug=system`;admin/ops_admin 住 system,权限平台级。tenant_admin/member 必须属于真实租户(store+API 校验)。`tenant_id NOT NULL`,查询与 claims 简单。

### 2.4 JWT:加 `tid`(租户 slug),旧 claims 保留一个迁移窗

- 新 claim `tid`;`orgSlug/tenantNs` 迁移窗内保留;`VerifyToken` 容忍无 `tid` 的旧 token;scope 回退 `tid=="" → tenantNs`。
- **即时禁用**:中间件可选挂 store,按请求查用户状态(`status='disabled'` → 401)与 `token_version`(改密即吊销在途 token)。开关 `AILS_AUTH_CHECK_DISABLED`(db 模式默认开)。

## 3. 数据模型(sqlite)

```sql
CREATE TABLE tenants (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  slug           TEXT NOT NULL UNIQUE,            -- 'hpc-lab';保留 'system'
  name           TEXT NOT NULL DEFAULT '',
  parent_account TEXT NOT NULL UNIQUE,            -- Slurm 父账号名
  status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
  created_at     TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,                    -- bcrypt;auth_source<>'local' 时为 ''
  role          TEXT NOT NULL CHECK (role IN ('admin','ops_admin','tenant_admin','member')),
  tenant_id     INTEGER NOT NULL REFERENCES tenants(id),
  cluster_user  TEXT NOT NULL UNIQUE,             -- unix 身份/Slurm 用户(L1)
  uid           INTEGER NOT NULL UNIQUE,          -- 2001.. 避让 991/64030/1001
  gid           INTEGER NOT NULL DEFAULT 2000,
  account       TEXT NOT NULL UNIQUE,             -- Slurm 叶子账号 == cluster_user
  display_name  TEXT NOT NULL DEFAULT '',
  email         TEXT NOT NULL DEFAULT '',
  auth_source   TEXT NOT NULL DEFAULT 'local' CHECK (auth_source IN ('local','ldap')),
  status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  token_version INTEGER NOT NULL DEFAULT 0,       -- ++ 吊销在途 JWT
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_users_tenant ON users(tenant_id, status);

CREATE TABLE audit_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  actor      TEXT NOT NULL,
  action     TEXT NOT NULL,                       -- 'user.create','user.disable','password.reset','tenant.create',...
  target     TEXT NOT NULL,                       -- 'user:foo' / 'tenant:bar'
  detail     TEXT NOT NULL DEFAULT '{}',
  request_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

`users` 列是 `auth.User` 的超集,yaml 导入为 1:1 列映射。

## 4. 组件变更

### 4.1 新包 `pkg/store`

```
pkg/store/store.go        // 接口 + 类型
pkg/store/sqlite.go       // sqlite 实现(modernc.org/sqlite)
pkg/store/migrate.go      // embed/*.sql 内嵌 schema + 迁移
pkg/store/import_yaml.go  // users.yaml → DB(迁移窗)
pkg/store/store_test.go
```

接口(窄读面让 `pkg/auth` 继续编译;LDAP 未来同面替换):

```go
type Tenants interface {
    CreateTenant(ctx, t Tenant) (Tenant, error)
    GetTenantBySlug(ctx, slug string) (Tenant, error)
    ListTenants(ctx) ([]Tenant, error)
    SetTenantStatus(ctx, slug, status string) error
}
type Users interface {
    auth.UserStore                                        // Lookup / Verify(既有面)
    CreateUser(ctx, u NewUser) (User, error)
    UpdateUser(ctx, username string, patch UserPatch) error
    SetPassword(ctx, username, bcryptHash string) error
    DisableUser(ctx, username string) error               // status + token_version++
    ListTenantUsers(ctx, tenantSlug string) ([]User, error)
    NextUID(ctx) (int, error)                             // 2001..2999
    ClusterUsersOfTenant(ctx, tenantSlug string) ([]string, error)
}
```

`auth.User` 加 `TenantSlug/DisplayName/Status/TokenVersion`;`auth.UserStore` 接口本身不变(内存库与 DB 库都满足,router_test 不动)。LDAP 缝:`UserStore.Verify` 是唯一凭证 choke point,`auth_source=='ldap'` 时返回 `ErrAuthSourceNotLocal`,未来 `pkg/auth/ldap.go` 实现同面+JIT 开户。

配置(`pkg/config`):`AILS_USER_STORE`(db|yaml,Phase 1 默认 yaml、Phase 5 翻 db)、`AILS_DB_PATH`、`AILS_AUTH_CHECK_DISABLED`。

### 4.2 `pkg/auth`

- `Claims` 加 `TID`;新增 `GenerateTokenClaims(claims)`,旧 `GenerateToken` 变薄包装(既有调用点不动)。
- `JWTAuthMiddlewareWithStore(store, checkDisabled)`:status/ver 校验失败 → 401(同文案防信息泄露)。
- **新 `pkg/auth/scope.go`**——租户策略唯一出处:

```go
type Scope struct {
    Mode        ScopeMode // ScopeSelf | ScopeTenant | ScopeAll
    TenantSlug  string    // claims.TID(迁移期回退 TenantNS)
    ClusterUser string
    Username    string
}
func ScopeFromClaims(c *gin.Context) Scope // admin/ops→All, tenant_admin→Tenant, member→Self
func (s Scope) AllowsUser(clusterUser string) bool
func (s Scope) AllowedUsersFilter() ([]string, bool) // nil,true = 全部
```

### 4.3 Handler 隔离(矩阵不变,执法入 handler)

| 文件 | 变更 |
|---|---|
| `pkg/services/billing/handler.go` | **member → 无视 `?user=`,强制本人**;tenant_admin → `?user=` 须属本租户(否则 403),缺省传本租户用户列表;ops/admin 不限。实现:`UsageQueryParam` 加 `AllowedUsers []string`,在 `GetUsage` 行级后过滤(SacctFetcher 不变,测试仍可注入) |
| `pkg/services/jobs/handler.go` | `ListJobs` 按 `scope.AllowsUser(Owner)` 过滤(空 owner=遗留放行);`forbidIfNotOwner`:tenant_admin 从通配改为本租户约束 |
| `pkg/services/containers/handler.go` | 同上(Owner 过滤 + 租户约束) |
| `pkg/auth/handler.go` | 新 `POST /api/v1/auth/password`(自助改密);`UserInfo` 加 `tenantSlug` |

### 4.4 新 `pkg/services/admin`(租户/用户管理 API)

接 `Handlers.Admin`;`RunInSlurmctld("sacctmgr",...)` 供运行时开户(幂等重试;DB 先提交,Provision 失败 502+可重试)。

### 4.5 前端

`slurm.ts` 镜像新端点/类型;`login.tsx` 移除装饰性 orgSlug 下拉(Phase 6);新 `routes/admin.tsx`(tenant_admin/admin 各自面板)。

### 4.6 Slurm 供给

`entrypoint.sh`:优先 DB 导出 JSON 种子(含租户父账号 + `parent=`),yaml 解析保留为迁移兜底。

## 5. 新 API 面

admin(平台,`RequireRole(admin)`):
```
POST   /api/v1/admin/tenants              {slug,name}
GET    /api/v1/admin/tenants
PATCH  /api/v1/admin/tenants/:slug        {name?,status?}
GET    /api/v1/admin/tenants/:slug/users
POST   /api/v1/admin/users                {username,role,tenantSlug,password}
```
tenant_admin(仅本租户):
```
GET    /api/v1/tenants/me/users
POST   /api/v1/tenants/me/users           {username,password,role:'member'|'tenant_admin'}  // 服务端定 tenant=claims.TID、clusterUser、uid、account;并 sacctmgr 开号
PATCH  /api/v1/tenants/me/users/:username {displayName?,email?,status?}   // 跨租户 404(防枚举)
POST   /api/v1/tenants/me/users/:username/password
```
自助:任何已认证角色 `POST /api/v1/auth/password`。
所有变更写 `audit_log`(actor/action/target/request_id)。

## 6. 角色矩阵(Phase 4 后权威)

| 能力 | member | tenant_admin | ops_admin | admin |
|---|---|---|---|---|
| 登录 | 本租户 active | 本租户 active | system | system |
| 读集群状态/监控 | 全部 | 全部 | 全部 | 全部 |
| **列作业/IDE 会话** | **仅自己** | **本租户** | 全部 | 全部 |
| 作业提交/控制 | 自己 | 本租户 | — | — |
| 容器 launch/list/回收 | 自己 | 本租户 | — | — |
| 计费 usage/export | **仅本人(强制)** | 本租户 | 全部 | —(无计费路由,不变) |
| 节点 DRAIN/RESUME | — | — | — | ✓ |
| 管理本租户用户 | — | ✓ | — | ✓(全部) |
| 管理租户/平台用户 | — | — | — | ✓ |
| 自助改密 | ✓ | ✓ | ✓ | ✓ |

路由层 `RequireRole` 门不变;矩阵变化全部经 `ScopeFromClaims` 在 handler 内实现 → `TestRouter_RouteMatrix` 保持有效。

## 7. 分期计划

| 期 | 内容 | 规模 | 验证 |
|---|---|---|---|
| **0** | **关 billing 漏洞** + `scope.go`(先经内存用户表,无 DB 依赖) | S | member + `?user=他人` → 只回本人;tenant_admin 跨租户 → 403 |
| 1 | sqlite store + yaml 导入(读路径,双模并存,默认 yaml) | M | 导入临时 DB 后 4 账号可登;router_test 不动全绿 |
| 2 | JWT `tid`+`ver` + 实时禁用检查 + 自助改密 | S/M | token 可见 tid;DB 禁用用户 → 在途 token 即刻 401;旧格式 token 兼容 |
| 3 | 租户/用户管理 API + 审计 + 前端 admin 页 | M | tenant_admin 建 alice → alice 登录提交作业以其 clusterUser 跑(L1/L3 不变) |
| 4 | 作业/会话租户隔离(矩阵完成) | M | 双租户测试:跨租户 member/tenant_admin 控制 → 403 |
| 5 | Slurm 租户父账号(fairshare)+ 翻默认 db | M | `sacctmgr show assoc tree` 呈树;`GrpTRES=cpu=2` 限额生效;billing 不变 |
| 6 | 退役 yaml + 清理(去 orgSlug 下拉等) | S | 空库冷启 → bootstrap admin → 全流程演练 |

零停机论证:1–2 双模配置切换;5 的 Slurm 变更是增量(加父账号/re-parent 不碰在跑作业与提交路径);DB 切换=无状态 apiserver 重启,登录中断秒级。

## 8. 风险

| 风险 | 缓解 |
|---|---|
| sqlite 写竞争/异常关机 | WAL+busy_timeout;单写者;`.backup` cron;store 接口留 MariaDB 逃生门 |
| `sacctmgr modify parent` 版本不支持 | Phase 5 先在 compose 栈验证;兜底 remove+re-add(DB 可重建关联) |
| 24h token vs 禁用/停租户 | `ver`+status 按请求检查(Phase 2),非 TTL 级 |
| 跨租户枚举泄露 | 跨租户目标 404 非 403;billing 跨租户 403 通用文案 |
| tenant_admin 通配收紧改变既有行为 | 目标本意;矩阵测试只增改 |
| 前后端类型漂移 | 同期镜像 slurm.ts |
| UID 冲突 | NextUID 2001–2999 + UNIQUE + Service 校验 |
| 运行时开户部分失败(DB 成功 Slurm 失败) | 幂等重试 + audit `provision_state` + entrypoint 重启对账 |

## 关键文件
- `pkg/auth/users.go`(User/UserStore 面)
- `pkg/auth/jwt.go`(Claims/GenerateToken — tid/ver)
- `pkg/auth/middleware.go`(+ scope.go)
- `cmd/apiserver/router.go`(路由表/矩阵/新 admin 组)
- `pkg/services/billing/handler.go`(Phase 0 第一现场)
- `deploy/slurm-cluster/config/entrypoint.sh`(Slurm 账号供给)
