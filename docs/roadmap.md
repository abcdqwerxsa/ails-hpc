# AILS HPC 后续开发路线图

制定于 2026-08-17(多租户 Phase 0-6 全部完成后)。每项沿用统一验证基线:
`go test ./...` 全绿 + `pnpm build` 绿 + 生产部署后按项 live 验证。

## 轨道 1:作业完整闭环(优先——核心工作流缺口)

| # | 内容 | 说明 | 规模 | 状态 |
|---|---|---|---|---|
| 1.1 | 表单补内存/GPU 申请 | 内存走 REST `memory_per_node`;GPU 在 slurm 21.08 REST 无提交字段(实测 `tres_per_node` 未知键、`gres` 被静默丢弃、`#SBATCH` 指令不解析)——走 `sudo -u <clusterUser> sbatch` CLI 路径(身份/记账实证无损)。GPU>0 强制 performance 分区 | M | **已完成**(PR #39+配置:REST 内存/GPU 走 sudo-sbatch CLI、GPU 记账 TRES 补 gres/gpu) |
| 1.2 | 作业输出管理 | 双路径统一 `output=/shared/jobs/%j.out`(stdout/stderr 合流,%j 实测展开)+cwd=/shared;entrypoint 备 1777 目录;详情端点+弹窗(sacct 生命期+tail-200,租户隔离,跨属主 404) | M | **已完成**(PR #41) |
| 1.3 | 作业历史页 | sacct 历史列表(倒序/按用户与状态过滤,复用 `Scope.RowFilter` 租户隔离);补齐"提交→运行→历史"生命周期 | M | 待做 |
| 1.4 | IDE 会话资源可调+时长续期 | 现固定 2CPU/4GB/2h | S | 待做 |

## 轨道 2:安全与运维加固

| # | 内容 | 说明 | 规模 | 状态 |
|---|---|---|---|---|
| 2.1 | 登录防爆破 | 失败计数锁定(内存+窗口)+ 失败审计 | S | 待做 |
| 2.2 | 审计查看器 | audit_log 已写不可见;admin 页审计表格(actor/action 过滤) | S | 待做 |
| 2.3 | 租户限额 UI | `grpTRES/fairshare` API 已有(Phase 5),admin 页补输入框 | S | 待做 |
| 2.4 | sqlite 备份 | `.backup` 到 /shared/backups 的 systemd timer | S | 待做 |
| 2.5 | JWT key 持久化 | named volume 承载 jwt_hs256.key,消除重建换 key 的 token 重铸 | S | 待做 |

## 轨道 3:可观测性增强

| # | 内容 | 规模 | 状态 |
|---|---|---|---|
| 3.1 | 节点异常告警横幅(DOWN/DRAIN + PENDING 堆积阈值,纯前端) | S | 待做 |
| 3.2 | 监控历史落盘(环形缓冲→sqlite,重启不清零;可选) | M | 待做 |
| 3.3 | 队列深度趋势线(监控页加 PENDING 序列) | S | 待做 |

## 轨道 4:集群能力扩展(按需)

| # | 内容 | 规模 | 状态 |
|---|---|---|---|
| 4.1 | 作业数组/依赖(--array/--dependency 高级选项) | M | 待做 |
| 4.2 | 预约/QOS 管理(admin 直通 sacctmgr) | M | 待做 |
| 4.3 | 多物理节点接入(provision 泛化) | L | 待做 |

## 执行顺序

**1.1 → 1.2 → 2.1~2.3(三个小件一批) → 1.3 → 2.4/2.5 → 3.x 按需 → 4.x 有真实需求再动**

理由:1.1/1.2 是每日硬缺口(GPU 申请不到、日志静默丢失);2.x 三件 S 级顺手闭环;
1.3 补齐生命周期;轨道 4 在有多节点/更多 GPU 前不动。
