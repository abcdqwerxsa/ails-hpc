# 测试就绪状态（TEST_READY.md）

> v4-W4 重写：旧文案宣称的 `test/e2e/` 套件从未入库（见 TEST_INFRA.md 历史注记）。
> 现行就绪状态以本文件为准。

## 基线

```bash
go test ./...    # 全量绿 = 就绪基线
```

## 当前覆盖（2026-08-18，v4）

- 权限矩阵：内置四角色 × 路由（`TestRouter_RouteMatrix`）+ 自定义角色逐权限
  （`TestRouteMatrix_CustomRoles`，含 partitions/users/quota 新面）
- 越权对抗：提权链、伪造 claims、跨租户、改派即刻生效、平台用户生命周期
  （禁用即吊销/重置强制改密/displayName）、租户配额 scope
- 运维旅程：`TestE2E_OperationalJourneys`（管理员/观测员/成员三链，覆盖 v2/v3/v4 面）
- store：迁移、角色 resync、审计保留裁剪、displayName、目录
- 服务层：作业/计费（费率 env 化）/预约/QOS/分区（命令语法与审计）
- 生产：`verify_rbac.sh` 部署后经 ssh 执行（最近一次 28/28，2026-08-18）

## 生产验证清单（部署后必跑）

1. `ssh root@192.168.20.226 'bash -s' < deploy/slurm-cluster/verify_rbac.sh`
2. 涉及计费/配额改动时：billing 页目检费率行与配额卡
3. 涉及审计保留改动时：`AILS_AUDIT_RETENTION_DAYS` 生效确认（journalctl 无报错）
