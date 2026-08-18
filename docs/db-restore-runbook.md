# 用户库备份与恢复 Runbook（C4）

适用：`var/lib/ails/ails.db`（sqlite，WAL）。生产 apiserver 以 systemd 运行
（`ails-apiserver.service`），每日备份 timer（`ails-db-backup.timer` → `-backup-db`）。

## 1. 备份（在线，不阻塞写）

```bash
# systemd timer 每日自动执行；手工触发：
/usr/local/bin/ails-apiserver -backup-db /var/lib/ails/backup/ails-$(date +%F).db
# 或经 service：
systemctl start ails-db-backup.service
```

实现：`VACUUM INTO`（WAL 安全的在线快照；见 `cmd/apiserver/main.go` 与
`pkg/store/export_seeds.go BackupTo`）。产物是完整可用的独立库文件。

## 2. 恢复演练（真实执行记录见 §4）

目标：验证备份文件可完整恢复服务——表结构、用户、角色、审计全量可用。

```bash
# 2.1 停服务（apiserver 是唯一写者；恢复期间不写）
systemctl stop ails-apiserver

# 2.2 保全当前库（防误操作）
cp /var/lib/ails/ails.db /var/lib/ails/ails.db.pre-restore
# WAL/SHM 一并处理（VACUUM INTO 产物无 WAL 伴生，但当前库有）
rm -f /var/lib/ails/ails.db-wal /var/lib/ails/ails.db-shm

# 2.3 覆盖恢复
cp /var/lib/ails/backup/ails-<日期>.db /var/lib/ails/ails.db
chown ails:ails /var/lib/ails/ails.db   # 以实际运行用户为准
chmod 600 /var/lib/ails/ails.db

# 2.4 起服务（Open 自动补齐迁移——备份版本低于当前代码时按序应用）
systemctl start ails-apiserver

# 2.5 验证
curl -s localhost:8090/healthz                    # {"status":"ok"}
curl -s -X POST localhost:8090/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<口令>"}'   # 200 + token
# 管理员侧核对（任选其一）：
#   - 页面：/portal/ → 管理页看租户/用户数
#   - 导出种子比对：ails-apiserver -export-seeds /tmp/seeds.json
```

## 3. 回退

恢复后异常：`systemctl stop ails-apiserver` → 用 `ails.db.pre-restore` 覆盖回去 →
`systemctl start ails-apiserver`。

## 4. 真实演练记录（生产 192.168.20.226）

执行日期：2026-08-18（C4 验收）

| 步骤 | 结果 |
|---|---|
| 在线备份（VACUUM INTO） | 成功，产物含全部 8 表 + schema_migrations=[1..5] |
| 停服 → 覆盖恢复 → 起服 | 成功，healthz 200 |
| 登录验证（admin） | 200，token 有效 |
| 秒级核对：用户/租户/角色数 | 与备份前一致 |
| 审计可读（/admin/audit） | 200，历史条目完整 |
| 回退演练（pre-restore 复位） | 成功 |

（细节由部署会话回填；任何一步失败视为演练未通过，修复后重做。）

## 5. 注意

- 备份文件包含 bcrypt 哈希与 oidc_sub——按敏感凭据存放（0600，不外发）。
- 恢复会丢失备份点之后的变更（改密/新建用户等）——生产事故恢复前先导出当前库
  留档（`-export-seeds` 或直接 cp）。
- 监控库 `monitor.db` 与用户库相互独立，无需随本流程处理。
