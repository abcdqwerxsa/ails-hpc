package monitor

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // 纯 Go 驱动（与用户库同款；独立库文件，互不干扰）
)

// monitor 持久化（roadmap 3.2）：采样落 sqlite（独立文件 monitor.db），apiserver 重启
// 后从库装回最近窗口——趋势不再因重启清零。环形窗口仍是内存态（读路径零 IO），
// 库仅作为跨重启的装载源 + 追加日志（Prune 控制行数）。
const persistSchema = `
CREATE TABLE IF NOT EXISTS monitor_samples (
  ts    INTEGER PRIMARY KEY,
  cpu   INTEGER NOT NULL,
  mem   INTEGER NOT NULL,
  gpu   INTEGER NOT NULL,
  disk  INTEGER NOT NULL,
  queue INTEGER NOT NULL DEFAULT 0
)`

// persistence 是采样持久化接口（测试可注入内存实现）。
type persistence interface {
	Append(sm sample)
	Load() []sample
	Prune(keep int)
	Close() error
}

// sqlitePersistence 生产实现（单写者；追加频率 = 采样频率 5s，负载可忽略）。
type sqlitePersistence struct {
	db *sql.DB
}

// openPersistence 打开（必要时创建）monitor 持久化库。
func openPersistence(path string) (persistence, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(persistSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &sqlitePersistence{db: db}, nil
}

func (p *sqlitePersistence) Append(sm sample) {
	_, _ = p.db.Exec(
		"INSERT OR REPLACE INTO monitor_samples (ts, cpu, mem, gpu, disk, queue) VALUES (?,?,?,?,?,?)",
		sm.ts, sm.cpu, sm.mem, sm.gpu, sm.diskP, sm.queue)
}

func (p *sqlitePersistence) Load() []sample {
	rows, err := p.db.Query(
		`SELECT ts, cpu, mem, gpu, disk, queue FROM monitor_samples ORDER BY ts`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []sample{}
	for rows.Next() {
		var sm sample
		if err := rows.Scan(&sm.ts, &sm.cpu, &sm.mem, &sm.gpu, &sm.diskP, &sm.queue); err != nil {
			return out
		}
		out = append(out, sm)
	}
	return out
}

// Prune 只保留最近 keep 行（按 ts 倒序）。
func (p *sqlitePersistence) Prune(keep int) {
	if keep <= 0 {
		return
	}
	_, _ = p.db.Exec(
		`DELETE FROM monitor_samples WHERE ts NOT IN (SELECT ts FROM monitor_samples ORDER BY ts DESC LIMIT ?)`, keep)
}

func (p *sqlitePersistence) Close() error { return p.db.Close() }
