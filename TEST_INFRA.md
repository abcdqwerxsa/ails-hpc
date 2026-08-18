# AILS HPC 测试架构（TEST_INFRA.md）

> v4-W4 重写（2026-08-18）。本文曾描述一套位于 `test/e2e/` 的影子 E2E 套件——
> 该套件从未提交入库，且其"内存平行实现路由"的路线与现行哲学冲突。现行架构：
> **一切测试驱动生产代码本身**（真实 NewRouter / 真实 sqlite / mock slurmrestd），
> 不维护任何平行实现。

## 分层与权威位置

| 层 | 位置 | 驱动对象 | 覆盖 |
|---|---|---|---|
| 单元/服务层 | `pkg/**/*_test.go` | 纯函数 + 注入桩（sacct fetcher / cluster runner / mock slurmrestd） | 解析、聚合、校验、命令语法 |
| store 层 | `pkg/store/*_test.go` | 真实 sqlite（TempDir 开库，真迁移） | 迁移、角色、用户、审计（含保留裁剪） |
| 路由矩阵 | `cmd/apiserver/router_test.go` | 生产 `NewRouter` | 四内置角色 × 关键路由鉴权矩阵、错误信封 |
| 自定义角色矩阵 | `cmd/apiserver/rbac_matrix_test.go` | 同上（sqlite 夹具） | 每权限点"恰好持有/恰好缺失"→200/403 |
| 越权对抗 | `cmd/apiserver/rbac_adversarial_test.go` | 同上 | 提权/伪造 claims/跨租户/改派即刻生效/生命周期/配额 scope |
| 运维旅程 | `cmd/apiserver/rbac_adversarial_test.go::TestE2E_OperationalJourneys` | 同上 | 同一令牌跨多面的端到端链路（v2/v3/v4） |
| 密码/会话策略 | `cmd/apiserver/policy_test.go`、`pkg/auth` | 真 store | 复杂度、历史、首登改密、会话台账 |
| 生产 live | `deploy/slurm-cluster/verify_rbac.sh` | 生产实例（192.168.20.226:8090） | 无副作用用例集：权限门、迁移版本、审计、前端可达 |

## 执行

```bash
go test ./...                                   # 全量（CI/本地基线）
ssh root@192.168.20.226 'bash -s' < deploy/slurm-cluster/verify_rbac.sh   # 生产验证（部署后）
```

## 原则

- **不建影子实现**：夹具全部走生产构造器（`NewRouter` / `store.Open` / `admin.NewService`），
  测试装配差异仅在注入面（runner/fetcher/mock server）。
- **权限矩阵三处同步**：改路由或权限点必须同步 `pkg/auth/permissions_test.go`（R1 冻结
  历史表）、`cmd/apiserver/rbac_matrix_test.go`、`docs/rbac-matrix.md`。
- **live 验证不打生产写面**：verify_rbac.sh 只含无副作用用例（403/400 断言与只读查询）。

## 历史注记

- 2026-08-17 曾存在一套由 agent track 生成的 `test/e2e/`（opaque-box + 进程内平行路由），
  未入库即失传；`TEST_READY.md` 当时宣称 100% 通过的即是它。现行架构有意不重建——
  平行路由会漂移于生产路由表，回归价值为负（router_test 头注"驱动生产路由表本身，
  而非 test/e2e 的内存平行实现"即此决策）。
