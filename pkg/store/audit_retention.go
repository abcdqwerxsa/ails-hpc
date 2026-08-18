package store

// v4-W2 审计保留：audit_log 只进不出无上限曾是哑弹。保留窗口默认 365 天
// （env AILS_AUDIT_RETENTION_DAYS 可调，<=0 显式禁用——仅建议有外部归档时）。
// 裁剪宁长勿短：审计是安全数据、误删不可恢复（备份之外无恢复通道），窗口只放大
// 不缩小。开库 prune 一次（幂等）+ 常驻每日 ticker；Close 即停。

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"time"
)

// auditRetentionDays 读 AILS_AUDIT_RETENTION_DAYS（缺省 365；非法值回落缺省）。
func auditRetentionDays() int {
	if v := os.Getenv("AILS_AUDIT_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 365
}

// pruneAudit 删除窗口外审计行，返回删除数。retainDays<=0 禁用（返回 0）。
// 实现注意（modernc 驱动实测）：DELETE 的 RowsAffected 不可靠（删除生效但计数
// 恒 0）；故先 COUNT 后 DELETE，按 COUNT 返回——单连接库且按龄删除只有本裁剪器
// 一个写者，两语句间无竞态窗口。截止时间在 Go 侧算好绑纯文本，不走
// datetime('now', ?) 函数+参数组合（部分查询形态下求值漂移）。
func (s *sqliteStore) pruneAudit(ctx context.Context, retainDays int) (int64, error) {
	if retainDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays).Format("2006-01-02 15:04:05")
	var n int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE created_at <= ?`, cutoff).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM audit_log WHERE created_at <= ?`, cutoff); err != nil {
		return 0, err
	}
	return n, nil
}

// startAuditPruner 启动保留裁剪：立即一次 + 每 24h 一次；stop 通道随 Close 关闭。
func (s *sqliteStore) startAuditPruner() {
	days := auditRetentionDays()
	if days <= 0 {
		return
	}
	stop := make(chan struct{})
	s.stopPruner = stop
	go func() {
		ctx := context.Background()
		_, _ = s.pruneAudit(ctx, days) // 开库先清一次（部署重启即触发，不等满 24h）
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_, _ = s.pruneAudit(ctx, days)
			case <-stop:
				return
			}
		}
	}()
}

// ensureAuditCreatedIndex 幂等建 created_at 索引（裁剪按时间扫全表的代价兜底；
// 走 Open 时 DDL 而非新迁移——CREATE IF NOT EXISTS 永久幂等，同 resyncBuiltinRoles 教义）。
func ensureAuditCreatedIndex(db *sql.DB) error {
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at)`)
	return err
}
